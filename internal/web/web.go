package web

import (
	"embed"
	"io/fs"
)

//go:embed templates.html static/*
var Assets embed.FS

func Static() fs.FS { root, _ := fs.Sub(Assets, "static"); return root }
