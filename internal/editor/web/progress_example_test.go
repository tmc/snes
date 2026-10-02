package web_test

import (
	"fmt"
	"github.com/tmc/snes/internal/editor/web"
	"net/http/httptest"
)

func ExampleProgressHandler() {
	handler := web.ProgressHandler(nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/progress", nil))
	fmt.Println(response.Code)
	// Output: 404
}
