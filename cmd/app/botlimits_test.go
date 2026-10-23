package main

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// Флаги бота: по умолчанию читает старый путь, плановый — только по явному флагу; shadow по умолчанию включён.
//
// Мутации: поменять дефолт FromPlan на true или Shadow на false; не читать BOT_LIMITS_FROM_PLAN — тест краснеет.
func TestBotLimitsMode(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		fromPlan bool
		shadow   bool
	}{
		{"пусто", nil, false, true},
		{"включён", map[string]string{"BOT_LIMITS_FROM_PLAN": "true"}, true, true},
		{"1", map[string]string{"BOT_LIMITS_FROM_PLAN": " 1 "}, true, true},
		{"shadow выключен", map[string]string{"BOT_LIMITS_SHADOW": "false"}, false, false},
		{"мусор — дефолт", map[string]string{"BOT_LIMITS_FROM_PLAN": "maybe", "BOT_LIMITS_SHADOW": "maybe"}, false, true},
		{"вебом не включается", map[string]string{"PLAN_WEB_ENABLED": "true"}, false, true},
	}
	for _, c := range cases {
		m := botLimitsMode(env(c.env))
		if m.FromPlan != c.fromPlan || m.Shadow != c.shadow {
			t.Errorf("%s: %+v, ждали fromPlan=%v shadow=%v", c.name, m, c.fromPlan, c.shadow)
		}
	}
}
