package org

// The organization's own look: a brand colour and a logo. Both are optional,
// and leaving them out is TeamUp's own indigo and mark.
//
// They are read before anybody signs in, because the sign-in page is the first
// thing that should look like the organization, so reading them takes no
// session. Changing them is for administrators.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

const brandingKey = "branding"

// MaxLogoBytes is the largest logo accepted. A logo is shown at 32 pixels high;
// anything bigger than this is a photograph, not a logo.
const MaxLogoBytes = 512 * 1024

// logoTypes are the pictures accepted as a logo. SVG is left out on purpose: it
// is a document that can carry script, and the logo is served from this origin.
var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Branding is what is stored.
type Branding struct {
	// Colour is "#rrggbb", or empty for the default.
	Colour string `json:"color"`
	// LogoType and Logo are the picture and its bytes, base64. Empty for none.
	LogoType string `json:"logoType,omitempty"`
	Logo     string `json:"logo,omitempty"`
}

// BrandingView is what is shown: the logo by address, not by content, so it is
// fetched once and cached rather than carried in every response.
type BrandingView struct {
	Name    string `json:"name"`
	Colour  string `json:"color"`
	LogoURL string `json:"logoUrl"`
}

func (b Branding) logoVersion() string {
	sum := sha256.Sum256([]byte(b.Logo))
	return hex.EncodeToString(sum[:6])
}

func (h *Handler) savedBranding(c *gin.Context, orgID uuid.UUID) (Branding, bool) {
	var saved Branding
	if err := h.service.GetConfigInto(c.Request.Context(), orgID, brandingKey, &saved); err != nil && !errors.Is(err, apperrors.ErrNotFound) {
		httpx.Error(c, err, "failed to read the organization's look")
		return Branding{}, false
	}
	return saved, true
}

func (h *Handler) brandingView(c *gin.Context, orgID uuid.UUID, b Branding) (BrandingView, bool) {
	o, err := h.service.GetOrganization(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to read the organization")
		return BrandingView{}, false
	}
	view := BrandingView{Name: o.Name, Colour: b.Colour}
	if b.Logo != "" {
		view.LogoURL = "/api/branding/logo?v=" + b.logoVersion()
	}
	return view, true
}

// instanceOrg is the organization a visitor with no session belongs to. TeamUp
// is one organization per installation.
func (h *Handler) instanceOrg(c *gin.Context) (uuid.UUID, bool) {
	id, err := h.service.FirstOrganizationID(c.Request.Context())
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			// Not set up yet: the default look.
			c.JSON(http.StatusOK, BrandingView{Name: "TeamUp"})
			return uuid.Nil, false
		}
		httpx.Error(c, err, "failed to resolve the organization")
		return uuid.Nil, false
	}
	return id, true
}

// GetBranding answers anybody, signed in or not.
func (h *Handler) GetBranding(c *gin.Context) {
	orgID, ok := h.instanceOrg(c)
	if !ok {
		return
	}
	saved, ok := h.savedBranding(c, orgID)
	if !ok {
		return
	}
	view, ok := h.brandingView(c, orgID, saved)
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, view)
}

// GetLogo serves the picture itself. The address carries its version, so a
// new logo is a new address and the old one can be cached for good.
func (h *Handler) GetLogo(c *gin.Context) {
	id, err := h.service.FirstOrganizationID(c.Request.Context())
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	saved, ok := h.savedBranding(c, id)
	if !ok {
		return
	}
	if saved.Logo == "" {
		c.Status(http.StatusNotFound)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(saved.Logo)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if c.Query("v") == saved.logoVersion() {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Data(http.StatusOK, saved.LogoType, raw)
}

// UpdateBranding saves the colour and the logo. A logo left out of the request
// is kept; an empty one removes it.
func (h *Handler) UpdateBranding(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	var req struct {
		Colour string `json:"color"`
		// Logo is a data URL ("data:image/png;base64,..."), "" to remove it,
		// or absent to keep it.
		Logo *string `json:"logo"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxLogoBytes*2)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That logo is too large, or the request is not valid."})
		return
	}
	saved, ok := h.savedBranding(c, orgID)
	if !ok {
		return
	}
	next := saved
	next.Colour = strings.TrimSpace(req.Colour)
	if next.Colour != "" && !hexColour.MatchString(next.Colour) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The colour must look like #4f46e5."})
		return
	}
	next.Colour = strings.ToLower(next.Colour)
	if req.Logo != nil {
		if *req.Logo == "" {
			next.Logo, next.LogoType = "", ""
		} else {
			kind, data, problem := parseLogo(*req.Logo)
			if problem != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": problem})
				return
			}
			next.Logo, next.LogoType = data, kind
		}
	}
	if err := h.service.SetConfig(c.Request.Context(), orgID, brandingKey, next, false); err != nil {
		httpx.Error(c, err, "failed to save the organization's look")
		return
	}
	view, ok := h.brandingView(c, orgID, next)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, view)
}

// parseLogo checks a data URL is a picture of an accepted kind and size, by its
// bytes rather than by what it says it is.
func parseLogo(dataURL string) (kind, data, problem string) {
	const prefix = "data:"
	rest, ok := strings.CutPrefix(dataURL, prefix)
	if !ok {
		return "", "", "The logo must be a picture."
	}
	meta, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return "", "", "The logo must be a picture."
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) == 0 {
		return "", "", "The logo could not be read."
	}
	if len(raw) > MaxLogoBytes {
		return "", "", "The logo must be 512 KB or smaller."
	}
	sniffed := http.DetectContentType(raw)
	if !logoTypes[sniffed] {
		return "", "", "The logo must be a PNG, JPEG, WebP or GIF picture."
	}
	return sniffed, base64.StdEncoding.EncodeToString(raw), ""
}

// RegisterPublicRoutes serves what needs no session.
func RegisterPublicRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)
	rg.GET("/branding", h.GetBranding)
	rg.GET("/branding/logo", h.GetLogo)
}
