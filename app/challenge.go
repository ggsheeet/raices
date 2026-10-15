package app

import (
	"net/url"
	"strings"
)

func challengeDisplay(raw string) (iframeURL, linkURL string) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	if mercadopagoHost(host) {
		return raw, ""
	}
	return "", raw
}

func mercadopagoHost(host string) bool {
	for _, root := range []string{"mercadopago.com.mx", "mercadopago.com.ar", "mercadopago.com"} {
		if host == root || strings.HasSuffix(host, "."+root) {
			return true
		}
	}
	return false
}
