package main

import "testing"

func TestEnvDefault(t *testing.T) {
	t.Setenv("SPEEDNAS_TEST_WERT", "aus-der-umgebung")

	// Leerer Schalter: die Umgebung greift.
	got := ""
	envDefault(&got, "SPEEDNAS_TEST_WERT")
	if got != "aus-der-umgebung" {
		t.Errorf("envDefault = %q", got)
	}

	// Gesetzter Schalter hat Vorrang - sonst könnte man im Container einen
	// Wert nicht mehr gezielt überschreiben.
	got = "vom-schalter"
	envDefault(&got, "SPEEDNAS_TEST_WERT")
	if got != "vom-schalter" {
		t.Errorf("Schalter wurde überschrieben: %q", got)
	}

	// Unbekannte Variable ändert nichts.
	got = ""
	envDefault(&got, "SPEEDNAS_GIBT_ES_NICHT")
	if got != "" {
		t.Errorf("unbekannte Variable setzte %q", got)
	}
}

func TestEnvDefaultBool(t *testing.T) {
	wahr := []string{"1", "true", "TRUE", "yes", "on", " on "}
	for _, v := range wahr {
		t.Setenv("SPEEDNAS_TEST_FLAG", v)
		got := false
		envDefaultBool(&got, "SPEEDNAS_TEST_FLAG")
		if !got {
			t.Errorf("%q wurde nicht als wahr erkannt", v)
		}
	}

	falsch := []string{"0", "false", "nein", "", "irgendwas"}
	for _, v := range falsch {
		t.Setenv("SPEEDNAS_TEST_FLAG", v)
		got := false
		envDefaultBool(&got, "SPEEDNAS_TEST_FLAG")
		if got {
			t.Errorf("%q wurde fälschlich als wahr erkannt", v)
		}
	}

	// Ein bereits gesetzter Schalter bleibt gesetzt, auch wenn die
	// Umgebungsvariable "aus" sagt.
	t.Setenv("SPEEDNAS_TEST_FLAG", "0")
	got := true
	envDefaultBool(&got, "SPEEDNAS_TEST_FLAG")
	if !got {
		t.Error("gesetzter Schalter wurde von der Umgebung zurückgesetzt")
	}
}
