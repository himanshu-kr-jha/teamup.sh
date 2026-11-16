package calls

// Deciding attendance in review, and taking attendance away as a spreadsheet.

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// seesAttendance is who may see attendance at all, the owner's decision of 16
// November: whoever manages everybody's meetings (owners and admins), and
// whoever is given See attendance or Review attendance. They see everybody;
// nobody else sees any, their own included.
func seesAttendance(c *gin.Context) bool {
	u := auth.GetUserFromContext(c)
	return u.Can(auth.PermMeetingsAll) || u.Can(auth.PermAttendanceSee) || u.Can(auth.PermAttendance)
}

func refuseAttendance(c *gin.Context) {
	c.JSON(http.StatusForbidden, gin.H{"error": "attendance is for administrators and whoever is given See attendance"})
}

// ReviewInput is a reviewer's decision: present, late (with LateMinutes),
// absent, on_leave or field_work. An empty decision takes one back.
type ReviewInput struct {
	Date        string    `json:"date" binding:"required"`
	UserID      uuid.UUID `json:"userId" binding:"required"`
	Decision    string    `json:"decision"`
	LateMinutes *int      `json:"lateMinutes"`
	Note        string    `json:"note"`
}

// DecideAttendance records a reviewer's decision about one person's day of a
// stand-up, replacing any earlier one, or takes it back.
func (s *Service) DecideAttendance(ctx context.Context, orgID, meetingID, reviewer uuid.UUID, in ReviewInput) error {
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "date must be YYYY-MM-DD")
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 1000 {
		return apperrors.New(apperrors.CodeBadRequest, "keep the note under 1000 characters")
	}
	if _, err := s.standingMeeting(ctx, orgID, meetingID); err != nil {
		return err
	}
	var inOrg bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2)`, in.UserID, orgID).Scan(&inOrg); err != nil {
		return fmt.Errorf("find person: %w", err)
	}
	if !inOrg {
		return apperrors.ErrNotFound
	}
	switch in.Decision {
	case "":
		_, err := s.db.Exec(ctx,
			`DELETE FROM attendance_reviews WHERE standing_meeting_id = $1 AND on_date = $2::date AND user_id = $3`,
			meetingID, in.Date, in.UserID)
		return err
	case "present", "absent", "on_leave", "field_work":
		in.LateMinutes = nil
	case "late":
		if in.LateMinutes == nil || *in.LateMinutes < 1 || *in.LateMinutes > 720 {
			return apperrors.New(apperrors.CodeBadRequest, "say how many minutes late, from 1 to 720")
		}
	default:
		return apperrors.New(apperrors.CodeBadRequest, "decision must be present, late, absent, on_leave or field_work")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO attendance_reviews (org_id, standing_meeting_id, on_date, user_id, decision, late_minutes, note, decided_by, decided_at)
		VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (standing_meeting_id, on_date, user_id) DO UPDATE
		SET decision = EXCLUDED.decision, late_minutes = EXCLUDED.late_minutes, note = EXCLUDED.note,
		    decided_by = EXCLUDED.decided_by, decided_at = EXCLUDED.decided_at
	`, orgID, meetingID, in.Date, in.UserID, in.Decision, in.LateMinutes, in.Note, reviewer, s.clock())
	if err != nil {
		return fmt.Errorf("record the decision: %w", err)
	}
	return nil
}

func (h *Handler) DecideAttendance(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	if !auth.GetUserFromContext(c).Can(auth.PermAttendance) {
		c.JSON(http.StatusForbidden, gin.H{"error": "reviewing attendance needs the Review attendance permission"})
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var in ReviewInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a date, a person and a decision are needed"})
		return
	}
	if err := h.service.DecideAttendance(c.Request.Context(), orgID, id, userID, in); err != nil {
		httpx.Error(c, err, "failed to record the decision")
		return
	}
	day, _ := time.Parse("2006-01-02", in.Date)
	days, err := h.service.AttendanceForEverybody(c.Request.Context(), orgID, id, userID, isAdmin(c), nil, day, day)
	if err != nil {
		httpx.Error(c, err, "failed to read attendance")
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": days})
}

// statusLabels is what a spreadsheet says for each status.
var statusLabels = map[string]string{
	"present":    "Present",
	"absent":     "Absent",
	"on_leave":   "Leave",
	"holiday":    "Holiday",
	"field_work": "Field work",
	"review":     "In review",
	"unknown":    "Unknown",
}

// cellLabel is a person's day in the spreadsheet: late is present, with by how much.
func cellLabel(a Attendee) string {
	if a.Status == "present" && a.MinutesLate > 0 {
		return fmt.Sprintf("Late %d min", a.MinutesLate)
	}
	return statusLabels[a.Status]
}

// summaryOrder is the totals, in the order the columns are written.
var summaryOrder = []string{"present", "absent", "on_leave", "holiday", "field_work", "review", "unknown"}

// attendanceGrid is people down the side and days across, with totals.
type attendanceGrid struct {
	header []string
	rows   [][]string
	// statuses holds each cell's status, for colouring.
	statuses [][]string
}

func gridOf(days []AttendanceDay) attendanceGrid {
	type person struct {
		name   string
		byDate map[string]string
		label  map[string]string
	}
	people := map[uuid.UUID]*person{}
	dates := []string{}
	for _, d := range days {
		dates = append(dates, d.Date)
		for _, a := range d.People {
			p := people[a.UserID]
			if p == nil {
				p = &person{name: a.Name, byDate: map[string]string{}, label: map[string]string{}}
				people[a.UserID] = p
			}
			p.byDate[d.Date] = a.Status
			p.label[d.Date] = cellLabel(a)
		}
	}
	ordered := make([]*person, 0, len(people))
	for _, p := range people {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return strings.ToLower(ordered[i].name) < strings.ToLower(ordered[j].name) })

	g := attendanceGrid{header: append([]string{"Name"}, dates...)}
	for _, key := range summaryOrder {
		g.header = append(g.header, statusLabels[key])
	}
	for _, p := range ordered {
		row := []string{p.name}
		statuses := []string{""}
		counts := map[string]int{}
		for _, date := range dates {
			status := p.byDate[date]
			row = append(row, p.label[date])
			statuses = append(statuses, status)
			counts[status]++
		}
		for _, key := range summaryOrder {
			row = append(row, fmt.Sprint(counts[key]))
			statuses = append(statuses, "")
		}
		g.rows = append(g.rows, row)
		g.statuses = append(g.statuses, statuses)
	}
	return g
}

func (g attendanceGrid) csv() ([]byte, error) {
	var buf bytes.Buffer
	// Excel reads a CSV as UTF-8 only with this mark, and names are not all ASCII.
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	if err := w.Write(g.header); err != nil {
		return nil, err
	}
	for _, row := range g.rows {
		if err := w.Write(safeCells(row)); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// safeCells keeps a name from being read as a formula by a spreadsheet.
func safeCells(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		if cell != "" && strings.ContainsAny(cell[:1], "=+-@\t\r") {
			cell = "'" + cell
		}
		out[i] = cell
	}
	return out
}

var cellFills = map[string]string{
	"present":    "D1FAE5",
	"absent":     "FEE2E2",
	"on_leave":   "E2E8F0",
	"holiday":    "FCE7F3",
	"field_work": "DBEAFE",
	"review":     "FEF3C7",
	"unknown":    "F3F4F6",
}

func (g attendanceGrid) xlsx(title string) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := "Attendance"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, err
	}
	fills := map[string]int{}
	for status, colour := range cellFills {
		style, err := f.NewStyle(&excelize.Style{
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{colour}},
			Alignment: &excelize.Alignment{Horizontal: "center"},
		})
		if err != nil {
			return nil, err
		}
		fills[status] = style
	}
	if err := f.SetCellStr(sheet, "A1", title); err != nil {
		return nil, err
	}
	_ = f.SetCellStyle(sheet, "A1", "A1", bold)
	for col, name := range g.header {
		cell, _ := excelize.CoordinatesToCellName(col+1, 3)
		if err := f.SetCellStr(sheet, cell, name); err != nil {
			return nil, err
		}
		_ = f.SetCellStyle(sheet, cell, cell, bold)
	}
	dates := len(g.header) - 1 - len(summaryOrder)
	for r, row := range g.rows {
		for col, value := range row {
			cell, _ := excelize.CoordinatesToCellName(col+1, r+4)
			if col > dates {
				// The totals are numbers, so they add up in the spreadsheet too.
				var n int
				_, _ = fmt.Sscan(value, &n)
				if err := f.SetCellInt(sheet, cell, int64(n)); err != nil {
					return nil, err
				}
				continue
			}
			if err := f.SetCellStr(sheet, cell, value); err != nil {
				return nil, err
			}
			if style, ok := fills[g.statuses[r][col]]; ok {
				_ = f.SetCellStyle(sheet, cell, cell, style)
			}
		}
	}
	_ = f.SetColWidth(sheet, "A", "A", 28)
	if len(g.header) > 1 {
		last, _ := excelize.ColumnNumberToName(len(g.header))
		_ = f.SetColWidth(sheet, "B", last, 12)
	}
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, XSplit: 1, YSplit: 3, TopLeftCell: "B4", ActivePane: "bottomRight"})
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportAttendance is the attendance the caller may read, for a range, as CSV or
// Excel: people down the side, dates across, and totals.
func (h *Handler) ExportAttendance(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var teamID *uuid.UUID
	if raw := c.Query("teamId"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid teamId"})
			return
		}
		teamID = &parsed
	}
	if !seesAttendance(c) {
		refuseAttendance(c)
		return
	}
	format := c.DefaultQuery("format", "xlsx")
	if format != "xlsx" && format != "csv" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be xlsx or csv"})
		return
	}
	from, to, ok := h.attendanceRange(c, orgID, id)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	days, err := h.service.AttendanceForEverybody(ctx, orgID, id, userID, isAdmin(c), teamID, from, to)
	if err != nil {
		httpx.Error(c, err, "failed to read attendance")
		return
	}
	m, err := h.service.standingMeeting(ctx, orgID, id)
	if err != nil {
		httpx.Error(c, err, "failed to read attendance")
		return
	}
	grid := gridOf(days)
	span := from.Format("2006-01-02")
	if !to.Equal(from) {
		span += " to " + to.Format("2006-01-02")
	}
	name := fileName(m.Name) + " attendance " + span
	var body []byte
	var kind string
	if format == "csv" {
		body, err = grid.csv()
		kind = "text/csv; charset=utf-8"
	} else {
		body, err = grid.xlsx(m.Name + ", attendance " + span)
		kind = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	if err != nil {
		httpx.Error(c, err, "failed to make the file")
		return
	}
	file := name + "." + format
	ascii := strings.Map(func(r rune) rune {
		if r > 126 {
			return '_'
		}
		return r
	}, file)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(file)))
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, kind, body)
}

// fileName keeps a meeting's name to what is safe in a download's name.
func fileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\' || r == '/' || r < 32:
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "Stand-up"
	}
	return out
}
