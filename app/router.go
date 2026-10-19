package app

import (
	"net/http"
	"time"

	"raices/database"
	"raices/helpers"
	"raices/payment"
	"raices/templates/view/auth"
	"raices/templates/view/register"
)

var logger Logger

type appState struct {
	db      *database.DB
	charger payment.Charger
	fetcher payment.Fetcher
	sender  Sender
	now     func() time.Time
}

var state appState

type Dependencies struct {
	Mux     *http.ServeMux
	MP      *payment.MPClient
	DB      *database.DB
	Logger  Logger
	Charger payment.Charger
	Fetcher payment.Fetcher
	Sender  Sender
	Now     func() time.Time
}

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
	Warning(format string, args ...any)
}

func Routes(deps Dependencies) {
	mux := deps.Mux
	logger = deps.Logger
	state = appState{
		db:      deps.DB,
		charger: deps.Charger,
		fetcher: deps.Fetcher,
		sender:  deps.Sender,
		now:     deps.Now,
	}
	if state.now == nil {
		state.now = time.Now
	}
	if state.charger == nil {
		state.charger = payment.NewCharger(deps.MP)
	}
	if state.fetcher == nil {
		state.fetcher = payment.NewFetcher(deps.MP)
	}
	if state.sender == nil {
		state.sender = noopSender{}
	}

	mux.HandleFunc("GET /{$}", handleRoot)
	mux.HandleFunc("GET /login", handleLoginPage)
	mux.HandleFunc("POST /login", handleLoginForm)
	mux.HandleFunc("GET /admin", handleAdmin)
	mux.HandleFunc("POST /admin/resend", handleAdminResend)
	mux.HandleFunc("GET /tickets/claim/{token}", handleClaim)
	mux.HandleFunc("GET /register", handleRegisterPage)
	mux.HandleFunc("GET /register/quantity", handleRegisterQuantity)
	mux.HandleFunc("POST /register/amount", handleRegisterAmount)
	mux.HandleFunc("POST /register/pay", handleRegisterPay)
	mux.HandleFunc("GET /register/status", handleRegisterStatus)
	mux.HandleFunc("GET /register/confirmed", handleRegisterConfirmed)
	mux.HandleFunc("GET /emanuel", handleRegisterPage)
	mux.HandleFunc("GET /emanuel/quantity", handleRegisterQuantity)
	mux.HandleFunc("POST /emanuel/amount", handleRegisterAmount)
	mux.HandleFunc("POST /emanuel/pay", handleRegisterPay)
	mux.HandleFunc("POST /webhooks/mercadopago", handleMercadoPagoWebhook)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/register", http.StatusSeeOther)
}

func handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if err := helpers.RenderPage(w, r, auth.Page("", r.URL.Query().Get("next"), helpers.NewFormValidationResult())); err != nil {
		logger.Error("render login page: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if err := helpers.RenderPage(w, r, register.Page(offerFor(r), helpers.NewFormValidationResult())); err != nil {
		logger.Error("render register page: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
