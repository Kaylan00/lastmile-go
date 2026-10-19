// Package handler é a função serverless da Vercel que serve a API da demo
// pública. A Vercel compila cada arquivo .go da pasta api/ como uma função e
// chama o Handler exportado.
//
// O vercel.json manda /api/* para esta função com o caminho original em
// ?path=..., e aqui o caminho é reconstruído antes de chamar as rotas da demo.
//
// A Vercel compila este arquivo como um pacote fora do módulo, então ele só
// pode importar pacotes públicos (por isso a demo fica em demo/, não em internal/).
package handler

import (
	"net/http"

	"github.com/Kaylan00/lastmile-go/demo"
)

var routes = demo.NewHandler()

// Handler é o ponto de entrada que a Vercel chama.
func Handler(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Query().Get("path"); p != "" {
		r.URL.Path = "/api/" + p
	}
	routes.ServeHTTP(w, r)
}
