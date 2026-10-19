// Package migrations guarda os arquivos SQL de cada serviço dentro do binário
// (go:embed). Assim a imagem Docker não precisa copiar a pasta junto.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed dispatch/*.sql tracking/*.sql
var files embed.FS

// Dispatch devolve as migrations do banco do dispatch-service.
func Dispatch() fs.FS { return sub("dispatch") }

// Tracking devolve as migrations do banco do tracking-service.
func Tracking() fs.FS { return sub("tracking") }

func sub(dir string) fs.FS {
	f, err := fs.Sub(files, dir)
	if err != nil {
		panic(err) // só acontece se o nome da pasta estiver errado no código
	}
	return f
}
