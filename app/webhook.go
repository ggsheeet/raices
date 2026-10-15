package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"raices/database/sqlc"
	"raices/env"
)

type webhookPayload struct {
	Type string `json:"type"`
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

func handleMercadoPagoWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	var payload webhookPayload
	_ = json.Unmarshal(body, &payload)

	dataID := strings.TrimSpace(r.URL.Query().Get("data.id"))
	if dataID == "" {
		dataID = strings.TrimSpace(payload.Data.ID)
	}
	topic := strings.TrimSpace(r.URL.Query().Get("type"))
	if topic == "" {
		topic = strings.TrimSpace(payload.Type)
	}
	requestID := strings.TrimSpace(r.Header.Get("x-request-id"))

	if !validWebhookSignature(r.Header.Get("x-signature"), dataID, requestID, env.MPWebhookSecret) {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	if topic != "order" {
		logger.Info("mercadopago webhook ignored topic %q", topic)
		w.WriteHeader(http.StatusOK)
		return
	}
	if dataID == "" {
		logger.Info("mercadopago webhook missing data.id")
		w.WriteHeader(http.StatusOK)
		return
	}
	if state.db == nil || state.fetcher == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	fetched, err := state.fetcher.Get(ctx, dataID)
	if err != nil || fetched == nil {
		logger.Error("mercadopago get %s: %v", dataID, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	order, ok, err := lookupWebhookOrder(ctx, fetched.ExternalReference, fetched.ID)
	if err != nil {
		logger.Error("webhook lookup: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if !ok {
		logger.Info("mercadopago webhook unknown order ref=%q mp=%q", fetched.ExternalReference, fetched.ID)
		w.WriteHeader(http.StatusOK)
		return
	}

	success := chargeSuccess(fetched.Status, fetched.StatusDetail)
	failure := terminalFailure(fetched.Status, fetched.StatusDetail)
	if !success && !failure {
		w.WriteHeader(http.StatusOK)
		return
	}

	now := state.now()
	var moved bool
	if err := state.db.InTx(ctx, func(q *sqlc.Queries) error {
		if err := ExpireStale(ctx, q, now); err != nil {
			return err
		}
		row, err := q.GetOrderByID(ctx, order.ID)
		if err != nil {
			return err
		}
		moved, err = Apply(ctx, q, now, row, success, fetched.TotalAmount, fetched.Currency)
		return err
	}); err != nil {
		logger.Error("webhook apply: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if moved {
		fresh, err := state.db.Queries.GetOrderByID(ctx, order.ID)
		if err != nil {
			logger.Error("webhook reload after apply: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if err := state.sender.Send(ctx, fresh); err != nil {
			logger.Error("webhook send: %v", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func lookupWebhookOrder(ctx context.Context, externalRef, mpOrderID string) (sqlc.Order, bool, error) {
	ref := strings.TrimSpace(externalRef)
	if ref != "" {
		id, err := strconv.ParseInt(ref, 10, 64)
		if err == nil {
			order, err := state.db.Queries.GetOrderByID(ctx, id)
			if err == nil {
				return order, true, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return sqlc.Order{}, false, err
			}
		}
	}
	mpOrderID = strings.TrimSpace(mpOrderID)
	if mpOrderID == "" {
		return sqlc.Order{}, false, nil
	}
	order, err := state.db.Queries.GetOrderByMpOrderID(ctx, sql.NullString{String: mpOrderID, Valid: true})
	if err == nil {
		return order, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.Order{}, false, nil
	}
	return sqlc.Order{}, false, err
}

func validWebhookSignature(header, dataID, requestID, secret string) bool {
	ts, v1, ok := parseSignatureHeader(header)
	if !ok {
		return false
	}
	want, err := hex.DecodeString(v1)
	if err != nil {
		return false
	}
	if signatureMatches(secret, dataID, requestID, ts, want) {
		return true
	}
	lower := strings.ToLower(dataID)
	return lower != dataID && signatureMatches(secret, lower, requestID, ts, want)
}

func signatureMatches(secret, dataID, requestID, ts string, want []byte) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(webhookManifest(dataID, requestID, ts)))
	return hmac.Equal(mac.Sum(nil), want)
}

func parseSignatureHeader(header string) (ts, v1 string, ok bool) {
	for _, part := range strings.Split(header, ",") {
		key, val, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "ts":
			ts = val
		case "v1":
			v1 = val
		}
	}
	return ts, v1, ts != "" && v1 != ""
}

func webhookManifest(dataID, requestID, ts string) string {
	var b strings.Builder
	if dataID != "" {
		b.WriteString("id:")
		b.WriteString(dataID)
		b.WriteByte(';')
	}
	if requestID != "" {
		b.WriteString("request-id:")
		b.WriteString(requestID)
		b.WriteByte(';')
	}
	b.WriteString("ts:")
	b.WriteString(ts)
	b.WriteByte(';')
	return b.String()
}
