// web/embed.go
package web

import "embed"

//go:embed index.html app.js annotate.html annotate.js projects.html projects.js project.html project.js trash.html trash.js pending.html pending.js mywork.html mywork.js users.html users.js login.html login.js common.js style.css
var Files embed.FS
