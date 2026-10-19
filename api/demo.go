// Package handler é a função serverless da Vercel que serve a API da demo
// pública. A Vercel compila cada arquivo .go da pasta api/ como uma função e
// chama o Handler exportado.
//
// O vercel.json manda /api/* para esta função com o caminho original em
// ?path=..., e aqui o caminho é reconstruído antes de chamar as rotas da demo.
package handler

import (
	"net/http"

	"github.com/Kaylan00/lastmile-go/internal/demo"
)

var routes = demo.NewHandler()

// Handler é o ponto de entrada que a Vercel chama.
func Handler(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Query().Get("path"); p != "" {
		r.URL.Path = "/api/" + p
	}
	routes.ServeHTTP(w, r)
}
