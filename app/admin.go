package app

import (
	"net/http"
	"strconv"

	"raices/helpers"
	"raices/templates/view/admin"
)

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	ok, err := requireSession(w, r)
	if err != nil {
		logger.Error("admin session: %v", err)
		return
	}
	if !ok {
		return
	}

	now := state.now()
	if err := ExpireStale(r.Context(), state.db.Queries, now); err != nil {
		logger.Error("admin expire stale: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	orders, err := state.db.Queries.ListOrders(r.Context())
	if err != nil {
		logger.Error("list orders: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	all := r.URL.Query().Get("filter") == "all"
	if !all {
		filtered := orders[:0]
		for _, order := range orders {
			if attentionOrder(order, now) {
				filtered = append(filtered, order)
			}
		}
		orders = filtered
	}

	if err := helpers.RenderPage(w, r, admin.Page(admin.PageView{All: all, Orders: orders})); err != nil {
		logger.Error("render admin: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func handleAdminResend(w http.ResponseWriter, r *http.Request) {
	ok, err := requireSession(w, r)
	if err != nil {
		logger.Error("admin resend session: %v", err)
		return
	}
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		logger.Error("admin resend parse form: %v", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(r.FormValue("order_id"), 10, 64)
	if err != nil {
		logger.Error("admin resend order id: %v", err)
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	order, loadErr := state.db.Queries.GetOrderByID(r.Context(), id)
	if loadErr != nil {
		logger.Error("admin resend load: %v", loadErr)
	} else if order.EmailStatus == "failed" {
		if sendErr := state.sender.Send(r.Context(), order); sendErr != nil {
			logger.Error("admin resend: %v", sendErr)
		}
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
