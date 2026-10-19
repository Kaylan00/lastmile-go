package order

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to Status
		want     bool
	}{
		{StatusPending, StatusAssigned, true},
		{StatusAssigned, StatusPickedUp, true},
		{StatusPickedUp, StatusOutForDelivery, true},
		{StatusOutForDelivery, StatusDelivered, true},
		{StatusOutForDelivery, StatusFailedAttempt, true},

		{StatusAssigned, StatusDelivered, false},       // pulou etapas
		{StatusPickedUp, StatusAssigned, false},        // voltou
		{StatusDelivered, StatusOutForDelivery, false}, // depois do fim
		{StatusFailedAttempt, StatusDelivered, false},  // fim é fim
		{StatusPending, StatusPending, false},          // mesmo status
		{StatusAssigned, Status("teleported"), false},  // status desconhecido
		{StatusPending, StatusFailedAttempt, false},    // falha antes de sair para entrega
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%s, %s) = %v, quero %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestSupersedes(t *testing.T) {
	tests := []struct {
		name              string
		current, incoming Status
		want              bool
	}{
		{"ordem normal", StatusAssigned, StatusPickedUp, true},
		{"pula etapa (evento do meio atrasou)", StatusAssigned, StatusOutForDelivery, true},
		{"evento atrasado é ignorado", StatusOutForDelivery, StatusPickedUp, false},
		{"duplicado é ignorado", StatusPickedUp, StatusPickedUp, false},
		{"nada muda depois de entregue", StatusDelivered, StatusFailedAttempt, false},
		{"nada muda depois da falha", StatusFailedAttempt, StatusDelivered, false},
		{"entregue direto de alocado", StatusAssigned, StatusDelivered, true},
		{"status desconhecido", StatusAssigned, Status("lost"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Supersedes(tt.current, tt.incoming); got != tt.want {
				t.Errorf("Supersedes(%s, %s) = %v, quero %v", tt.current, tt.incoming, got, tt.want)
			}
		})
	}
}

func TestStatusHelpers(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusAssigned, StatusPickedUp, StatusOutForDelivery, StatusDelivered, StatusFailedAttempt} {
		if !s.Valid() {
			t.Errorf("%s deveria ser válido", s)
		}
	}
	if Status("x").Valid() {
		t.Error("status desconhecido não deveria ser válido")
	}
	if !StatusDelivered.Terminal() || !StatusFailedAttempt.Terminal() || StatusOutForDelivery.Terminal() {
		t.Error("Terminal() errado")
	}
	if !StatusPickedUp.Active() || StatusPending.Active() || StatusDelivered.Active() {
		t.Error("Active() errado")
	}
}
