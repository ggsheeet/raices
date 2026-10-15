package admin

import (
	"fmt"
	"strconv"

	"raices/database/sqlc"
)

type PageView struct {
	All    bool
	Orders []sqlc.Order
}

func buyerName(order sqlc.Order) string {
	return order.GivenName + " " + order.FamilyName
}

func amountLabel(cents int64) string {
	return fmt.Sprintf("Mex$%d.%02d", cents/100, cents%100)
}

func orderIDLabel(id int64) string {
	return strconv.FormatInt(id, 10)
}

func mpOrderID(order sqlc.Order) string {
	if !order.MpOrderID.Valid {
		return ""
	}
	return order.MpOrderID.String
}

func emailError(order sqlc.Order) string {
	if !order.EmailLastError.Valid {
		return ""
	}
	return order.EmailLastError.String
}
