package app

import (
	"net/http"
	"raices/helpers"
	"raices/payment"
	"raices/templates/view/auth"
	"raices/templates/view/register"
)

var logger Logger
var mp *payment.MPClient

type Dependencies struct {
	Mux    *http.ServeMux
	MP     *payment.MPClient
	Logger Logger
}

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
	Warning(format string, args ...any)
}

func Routes(deps Dependencies) {
	mux := deps.Mux
	mp = deps.MP
	logger = deps.Logger

	mux.HandleFunc("GET /{$}", handleRoot)
	mux.HandleFunc("GET /login", handleLoginPage)
	mux.HandleFunc("POST /login", handleLoginForm)
	mux.HandleFunc("GET /register", handleRegisterPage)
	mux.HandleFunc("GET /register/quantity", handleRegisterQuantity)
	mux.HandleFunc("POST /register/amount", handleRegisterAmount)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/register", http.StatusSeeOther)
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if err := helpers.RenderPage(w, r, auth.Page("", helpers.NewFormValidationResult())); err != nil {
		logger.Error("render login page: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if err := helpers.RenderPage(w, r, register.Page(helpers.NewFormValidationResult())); err != nil {
		logger.Error("render register page: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
