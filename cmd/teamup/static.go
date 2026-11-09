package main

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerStatic serves the built frontend: every folder by its own name (assets,
// icons, sounds, call backgrounds, the MediaPipe runtime) and every file at its
// root (the service worker, the manifest).
//
// Both used to be listed by hand, one route per name, including Workbox's file,
// whose name carries a content hash. Anything the build added or renamed was
// answered with the app's index.html instead of itself, which for a service worker
// means a script that silently is not there.
func registerStatic(router gin.IRoutes, site fs.FS) {
	entries, err := fs.ReadDir(site, ".")
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			router.StaticFileFS("/"+name, name, http.FS(site))
			continue
		}
		dir, err := fs.Sub(site, name)
		if err != nil {
			continue
		}
		cache := staticCache(name)
		files := http.StripPrefix("/"+name, http.FileServer(http.FS(dir)))
		serve := func(c *gin.Context) {
			cache(c)
			files.ServeHTTP(c.Writer, c.Request)
		}
		router.GET("/"+name+"/*filepath", serve)
		router.HEAD("/"+name+"/*filepath", serve)
	}
}

// staticCache says how long a browser may keep a built folder's files. Embedded
// files have no modification time, so without this a browser would fetch them
// again every time; that matters for the MediaPipe runtime, about 9 MB, fetched
// when a background effect starts. Its folder is named by version, and assets'
// names carry a content hash, so both can be kept for good. The rest (sounds,
// backgrounds, the model, icons) keep their names across releases, so a day.
func staticCache(folder string) func(*gin.Context) {
	return func(c *gin.Context) {
		value := "public, max-age=86400"
		if folder == "assets" || (folder == "mediapipe" && strings.Contains(c.Request.URL.Path, "/tasks-vision-")) {
			value = "public, max-age=31536000, immutable"
		}
		c.Header("Cache-Control", value)
	}
}
