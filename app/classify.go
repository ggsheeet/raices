package app

import "raices/payment"

func chargeFailed(res payment.Result) bool {
	if res.Kind == payment.KindNoOrder {
		return true
	}
	if res.Kind != payment.KindOrder {
		return false
	}
	return terminalFailure(res.Status, res.StatusDetail)
}

func chargeSuccess(status, detail string) bool {
	if status != "processed" {
		return false
	}
	return detail == "accredited" || detail == "partially_refunded"
}

func terminalFailure(status, detail string) bool {
	switch status {
	case "failed":
		return detail == "failed" || detail == "cc_rejected_3ds_challenge"
	case "canceled":
		return detail == "canceled" || detail == "expired"
	case "expired":
		return detail == "expired"
	case "refunded":
		return detail == "refunded"
	case "charged_back":
		return detail == "in_process" || detail == "settled" || detail == "reimbursed"
	default:
		return false
	}
}

func isChallenge(status, detail string) bool {
	return status == "action_required" && detail == "pending_challenge"
}
