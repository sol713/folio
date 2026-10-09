package folio

import (
	"embed"
	"io/fs"
)

//go:embed web/index.html web/styles.css web/app.js web/i18n.js
var embedded embed.FS

func Assets() fs.FS { f, _ := fs.Sub(embedded, "web"); return f }
