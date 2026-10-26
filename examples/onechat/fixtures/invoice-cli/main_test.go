package main

import "testing"

func TestOneChatExampleInvoice(t *testing.T) {
	invoice := ExampleInvoice()
	if invoice.Number != "INV-001" || len(invoice.Lines) != 1 || invoice.Lines[0].Quantity != 2 || invoice.Lines[0].UnitPriceCents != 5000 {
		t.Fatalf("unexpected example invoice: %+v", invoice)
	}
}
