package otomax

import "strings"

// BuildPesanFromTemplate fills format template with values from request DigiFlazz.
// Format disimpan di .env (OTOMAX_INSERTINBOX_PESAN_FORMAT). Placeholder: {{ref_id}}, {{hp}}, {{pulsa_code}}, {{price}}, {{username}}, {{commands}}.
// Contoh format: "{{pulsa_code}}.{{hp}}.{{price}}.{{ref_id}}" → "xld25.087800001233.25100.some1d"
func BuildPesanFromTemplate(format string, values map[string]string) string {
	if format == "" {
		return ""
	}
	out := format
	for k, v := range values {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}
