package main

import "testing"

func TestGenerateRefusesUnknownNames(t *testing.T) {
	if err := runKeysGenerate([]string{"NOT_A_KEY"}); err == nil {
		t.Fatal("an unknown key name must be refused")
	}
}
