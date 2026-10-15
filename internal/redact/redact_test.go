package redact

import "testing"

func TestText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"email", "scrivi a mario.rossi+x@example.com ok", "scrivi a [EMAIL] ok"},
		{"mobile plain", "il mio numero 333 123 4567 grazie", "il mio numero [PHONE] grazie"},
		{"mobile compact", "chiamami 3331234567", "chiamami [PHONE]"},
		{"mobile dots", "tel 333.123.4567", "tel [PHONE]"},
		{"mobile after label dot", "Cell.3331234567; N", "Cell.[PHONE]; N"},
		{"mobile +39", "+39 333 123 4567", "[PHONE]"},
		{"mobile 0039", "0039-333-1234567.", "[PHONE]."},
		{"intl", "call +44 20 7946 0958 now", "call [PHONE] now"},
		{"landline", "tel 02 12345678", "tel [PHONE]"},
		{"cf", "CF rssmra80a01h501u fine", "CF [CF] fine"},
		{"iban IT", "IBAN IT60X0542811101000000123456 ok", "IBAN [IBAN] ok"},
		{"iban spaced", "DE89 3704 0044 0532 0130 00", "[IBAN]"},
		{"card luhn", "carta 4111 1111 1111 1111 scade", "carta [CARD] scade"},
		{"card dashes", "4111-1111-1111-1111", "[CARD]"},
		{"address", "abito in via Roma 12, 20100 Milano", "abito in [ADDRESS]"},
		{"address civ", "Viale dei Mille n. 5", "[ADDRESS]"},
		{"address piazza", "piazza Garibaldi 3b", "[ADDRESS]"},
		{"order off", "ordine F123456789", "ordine F123456789"},
		{"neg price", "costa 19.99 € invece di 24,90 €", "costa 19.99 € invece di 24,90 €"},
		{"neg price big", "totale 1.299,00 euro", "totale 1.299,00 euro"},
		{"neg qty", "ne voglio 3 confezioni, 2 pezzi", "ne voglio 3 confezioni, 2 pezzi"},
		{"neg minsan", "prodotto 912345678 disponibile", "prodotto 912345678 disponibile"},
		{"neg minsan 3", "minsan 312345678", "minsan 312345678"},
		{"neg ean", "EAN 8033456789012", "EAN 8033456789012"},
		{"neg date", "il 15/10/2026 e 2026-10-15 o 10.10.2026", "il 15/10/2026 e 2026-10-15 o 10.10.2026"},
		{"neg pct", "sconto del 20% fino al 30%", "sconto del 20% fino al 30%"},
		{"neg via mail", "ti rispondo via mail 3 volte", "ti rispondo via mail 3 volte"},
		{"neg non-luhn", "1234 5678 9012 3456", "1234 5678 9012 3456"},
		{"neg time", "aperto 09:00-19:00", "aperto 09:00-19:00"},
		{"iban lowercase", "it60x0542811101000000123456", "[IBAN]"},
		{"iban lowercase spaced", "IBAN it60 x054 2811 1010 0000 0123 456", "IBAN [IBAN]"},
		{"landline parens", "(02) 1234567", "[PHONE]"},
		{"landline parens label", "chiamo il (02) 1234567 ok", "chiamo il [PHONE] ok"},
		{"mobile odd grouping", "333 12 34 567", "[PHONE]"},
		{"landline compact", "tel 0123456789", "tel [PHONE]"},
		{"cf spaced", "rss mra 80a01 h501u", "[CF]"},
		{"neg date dashes", "ricevuto il 01-10-2026 grazie", "ricevuto il 01-10-2026 grazie"},
		{"neg short zero", "codice 0123 e 02 44", "codice 0123 e 02 44"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in, Options{}); got != c.want {
				t.Errorf("Text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMaskOrders(t *testing.T) {
	if got := Text("ordine F123456789 ok", Options{MaskOrders: true}); got != "ordine [ORDER] ok" {
		t.Errorf("got %q", got)
	}
	if got := Text("F12345", Options{MaskOrders: true}); got != "F12345" {
		t.Errorf("got %q", got)
	}
}

func TestStripHTML(t *testing.T) {
	in := "<p>Ciao <b>Mario</b>,<br>costa 5 &amp; 6 &euro;</p><p>ok</p>"
	want := "Ciao Mario,\ncosta 5 & 6 €\nok"
	if got := StripHTML(in); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := StripHTML("plain"); got != "plain" {
		t.Error(got)
	}
}
