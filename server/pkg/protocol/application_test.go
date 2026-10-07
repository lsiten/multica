package protocol

import "testing"

func TestApplicationConnectionEnvironmentBindingsRejectAmbiguity(t *testing.T) {
	config := DefaultApplicationConfig()
	config.ResourceID = "resource"
	config.Command = []string{"test-program"}
	config.Port = 4100
	config.Connections = []ApplicationConnection{{TargetID: "11111111-1111-4111-8111-111111111111", URLVariable: "API_URL"}}
	if err := config.Validate("service"); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"MULTICA_TOKEN", "PORT", "invalid name"} {
		copy := config
		copy.Connections = []ApplicationConnection{{TargetID: config.Connections[0].TargetID, URLVariable: variable}}
		if copy.Validate("service") == nil {
			t.Fatalf("unsafe binding variable accepted: %s", variable)
		}
	}
	config.Environment["API_URL"] = "override"
	if config.Validate("service") == nil {
		t.Fatal("connection variable overlaps a configured environment value")
	}
}

func TestApplicationConfigValidation(t *testing.T) {
	managed := func() ApplicationConfig {
		c := DefaultApplicationConfig()
		c.ResourceID = "resource"
		c.Command = []string{"node", "server.js"}
		c.Port = 3100
		return c
	}
	cases := []struct {
		name  string
		kind  string
		edit  func(*ApplicationConfig)
		valid bool
	}{
		{"managed", "service", func(c *ApplicationConfig) {}, true},
		{"background worker", "service", func(c *ApplicationConfig) { c.Port = 0; c.Health.Kind = "none" }, true},
		{"external", "service", func(c *ApplicationConfig) { c.Mode = "external"; c.Command = nil }, true},
		{"external process control", "service", func(c *ApplicationConfig) { c.Mode = "external" }, false},
		{"missing resource", "service", func(c *ApplicationConfig) { c.ResourceID = "" }, false},
		{"escape", "service", func(c *ApplicationConfig) { c.WorkDir = "../other" }, false},
		{"absolute path", "service", func(c *ApplicationConfig) { c.WorkDir = "/etc" }, false},
		{"windows drive path", "service", func(c *ApplicationConfig) { c.WorkDir = "C:/Windows" }, false},
		{"nested path", "service", func(c *ApplicationConfig) { c.WorkDir = "apps/web" }, true},
		{"no port for health", "service", func(c *ApplicationConfig) { c.Port = 0 }, false},
		{"remote health", "service", func(c *ApplicationConfig) { c.Health.Kind = "http"; c.Health.Path = "//attacker.test" }, false},
		{"retry storm", "service", func(c *ApplicationConfig) { c.Restart.Enabled = true; c.Restart.MaxAttempts = 11 }, false},
		{"local credential reference", "service", func(c *ApplicationConfig) { c.LocalEnv["API_KEY"] = "APP_API_KEY" }, true},
		{"daemon credential reference", "service", func(c *ApplicationConfig) { c.LocalEnv["API_KEY"] = "MULTICA_TASK_TOKEN" }, false},
		{"daemon credential override", "service", func(c *ApplicationConfig) { c.Environment["MULTICA_TOKEN"] = "value" }, false},
		{"duplicate variable", "service", func(c *ApplicationConfig) { c.Environment["TOKEN"] = "value"; c.LocalEnv["TOKEN"] = "LOCAL_TOKEN" }, false},
		{"unbounded prepare", "service", func(c *ApplicationConfig) { c.Prepare = []ApplicationCommand{{Args: []string{"make"}}} }, false},
		{"composition process", "composition", func(c *ApplicationConfig) {}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := managed()
			tc.edit(&c)
			if err := c.Validate(tc.kind); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	if err := DefaultApplicationConfig().Validate("composition"); err != nil {
		t.Fatal(err)
	}
}
