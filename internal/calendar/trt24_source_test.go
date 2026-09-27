package calendar

import (
	"strings"
	"testing"
)

func TestPrepareTRT24SeparatesLocalDaysAndPartialClosures(t *testing.T) {
	html := `<table><tr><th>Data</th><th>Localidade</th><th>Descrição</th><th>Legislação</th></tr>
<tr><td>01/01/2026</td><td>Nacional</td><td>Confraternização</td><td>Lei 1</td></tr>
<tr><td>18/02/2026</td><td>Tribunal</td><td>Suspensão expediente até as 13h</td><td>Portaria 2</td></tr>
<tr><td>11/05/2026</td><td>Nova Andradina</td><td>Feriado local</td><td>Lei 3</td></tr></table>`
	drafts, err := PrepareTRT24(strings.NewReader(html), 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 2 {
		t.Fatalf("drafts: %+v", drafts)
	}
	for _, d := range drafts {
		if d.ScopeType == "TRIBUNAL" && (len(d.Days) != 2 || d.Days[1].Effect != "PARTIAL") {
			t.Fatalf("tribunal days: %+v", d.Days)
		}
		if d.ScopeKey == "NOVA ANDRADINA" && (len(d.Days) != 1 || d.Days[0].Effect != "CLOSED") {
			t.Fatalf("local days: %+v", d.Days)
		}
	}
}
