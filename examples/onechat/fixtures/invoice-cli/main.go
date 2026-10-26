package main

import "fmt"

type Line struct {
	Description    string
	Quantity       int
	UnitPriceCents int
}

type Invoice struct {
	Number string
	Lines  []Line
}

func ExampleInvoice() Invoice {
	return Invoice{Number: "INV-001", Lines: []Line{{Description: "Consulting", Quantity: 2, UnitPriceCents: 5000}}}
}

func main() {
	invoice := ExampleInvoice()
	fmt.Printf("Invoice %s\n", invoice.Number)
	for _, line := range invoice.Lines {
		fmt.Printf("%d x %s @ %d cents\n", line.Quantity, line.Description, line.UnitPriceCents)
	}
}
