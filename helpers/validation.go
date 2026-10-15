package helpers

import "context"

type FormValidationError struct {
	Field   string
	Message string
}

type FormValidationResult struct {
	Errors []FormValidationError
	Valid  bool
}

func NewFormValidationResult(errs ...FormValidationError) FormValidationResult {
	return FormValidationResult{Errors: errs, Valid: len(errs) == 0}
}

func GetFormErrorMessage(result FormValidationResult, field string) string {
	for _, err := range result.Errors {
		if err.Field == field {
			return err.Message
		}
	}
	return ""
}

func FormErrorText(_ context.Context, key string) string {
	if key == "" {
		return ""
	}
	if text, ok := formErrorText[key]; ok {
		return text
	}
	return key
}

var formErrorText = map[string]string{
	"email_required":       "Enter an email address.",
	"email_invalid":        "Enter a valid email address.",
	"password_required":    "Enter a password.",
	"password_short":       "Use at least 8 characters.",
	"credentials_wrong":    "Correo o contraseña incorrectos.",
	"request_invalid":    "That submission could not be read.",
	"request_unreadable": "No se pudo leer el formulario.",
	"qty_required":       "Selecciona la cantidad de boletos.",
	"qty_invalid":        "Ingresa una cantidad válida.",
	"qty_range":          "Selecciona entre 1 y 10 boletos.",
	"name_required":      "Escribe tu nombre y apellido.",
	"name_too_long":      "Usa como máximo 100 caracteres.",
	"email_mismatch":     "Los correos no coinciden.",
	"age_invalid":        "Ingresa una edad válida.",
	"age_range":          "La edad debe ser entre 1 y 120.",
	"token_required":     "No se pudo tokenizar la tarjeta.",
}
