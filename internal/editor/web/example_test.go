package web_test

import (
	"fmt"
	"github.com/tmc/snes/internal/editor/web"
	"net/http/httptest"
)

func ExampleHandler() {
	w := httptest.NewRecorder()
	web.Handler(&web.Model{Baseline: "unavailable"}).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	fmt.Println(w.Code)
	// Output: 200
}
