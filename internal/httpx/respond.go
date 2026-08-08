// Package httpx holds the small HTTP conventions shared by every feature
// package, so a service error turns into the same response shape everywhere.
package httpx

import (
	"github.com/gin-gonic/gin"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Error writes err with the status its code implies. Client errors carry their
// own message; server errors are replaced by fallback so internal detail —
// constraint names, driver text — never reaches the caller.
func Error(c *gin.Context, err error, fallback string) {
	status := apperrors.HTTPStatus(err)
	c.JSON(status, gin.H{"error": apperrors.PublicMessage(err, fallback)})
}
