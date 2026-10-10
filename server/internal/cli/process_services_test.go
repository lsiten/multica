package cli

import "testing"

func TestValidateProcessServices(t *testing.T) {
	t.Run("accepts the exposed opt-in roles", func(t *testing.T) {
		if err := ValidateProcessServices([]string{"ai", "mirror", "application", "environment", "gateway"}); err != nil {
			t.Fatalf("supported roles rejected: %v", err)
		}
	})
	t.Run("empty keeps the legacy default", func(t *testing.T) {
		if err := ValidateProcessServices(nil); err != nil {
			t.Fatalf("empty set rejected: %v", err)
		}
	})
	t.Run("rejects an unexposed role", func(t *testing.T) {
		if err := ValidateProcessServices([]string{"environment", "taskworker"}); err == nil {
			t.Fatal("unexposed role accepted")
		}
	})
	t.Run("rejects a duplicate role", func(t *testing.T) {
		if err := ValidateProcessServices([]string{"environment", "environment"}); err == nil {
			t.Fatal("duplicate role accepted")
		}
	})
}
