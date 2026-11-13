// web/embed.go
package web

import "embed"

//go:embed index.html app.js annotate.html annotate.js projects.html projects.js project.html project.js trash.html trash.js common.js style.css
var Files embed.FS
