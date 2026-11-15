package presence

import (
	"context"
	"testing"
	"time"

	"github.com/teamup/teamup/internal/leave"
	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
)

func TestLeaveSomebodysManagerAddedShowsThemOnLeaveAndBackWhenItEnds(t *testing.T) {
	pool := testsupport.DB(t)
	svc := NewService(testRedis(t))
	svc.SetDB(pool)
	leaves := leave.NewService(pool, tasks.NewService(pool))
	svc.SetLeave(leaves)
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	ann := testsupport.SeedUser(t, pool, org, "ann@example.test", "member")
	today := time.Now().UTC().Format("2006-01-02")
	last := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	if _, err := leaves.Add(ctx, org.ID, leave.Actor{ID: org.OwnerID, Admin: true}, leave.Input{UserID: ann, StartsOn: today, EndsOn: &last}); err != nil {
		t.Fatal(err)
	}

	roster, err := svc.ListOrgAvailability(ctx, org.ID, today)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range roster {
		if p.UserID != ann {
			continue
		}
		back := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
		if p.Status != "on_leave" || p.Until == nil || p.Until.Format("2006-01-02") != back {
			t.Errorf("Ann is on leave, back in three days, got %+v", p)
		}
	}

	// She says she is back: the leave ends yesterday and she is available.
	if err := svc.ChooseAvailability(ctx, org.ID, ann, Availability{Status: "available"}, today); err != nil {
		t.Fatal(err)
	}
	roster, _ = svc.ListOrgAvailability(ctx, org.ID, today)
	for _, p := range roster {
		if p.UserID == ann && p.Status == "on_leave" {
			t.Errorf("she is back, got %+v", p)
		}
	}
}
