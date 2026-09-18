package appcontrol

import (
	"context"
	"testing"
)

func TestListAppsRequiresAuthorityAndBoundsNativeReply(t *testing.T) {
	for _, tc := range []string{"valid", "foreign", "malformed", "overflow"} {
		t.Run(tc, func(t *testing.T) {
			c, b, a, _ := controlFixture(t)
			b.run = func(_ context.Context, op string, in, out any) error {
				if op != "list_apps" {
					t.Fatalf("unexpected native operation %s", op)
				}
				v := out.(*AppList)
				v.Apps = []InstalledApp{{BundleID: "org.example.Editor", Name: "Editor"}}
				if tc == "malformed" {
					v.Apps[0].BundleID = ""
				}
				if tc == "overflow" {
					v.Apps = make([]InstalledApp, 513)
				}
				return nil
			}
			if tc == "foreign" {
				a.Resource.RuntimeID = "other"
			}
			got, err := c.ListApps(t.Context(), a)
			if tc == "valid" {
				if err != nil || len(got.Apps) != 1 {
					t.Fatalf("got %+v, %v", got, err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid inventory or authority")
			}
			if tc == "foreign" && len(b.calls) != 0 {
				t.Fatal("foreign authority reached native inventory")
			}
		})
	}
}

func TestObservationCertificationComesFromHostPolicy(t *testing.T) {
	c, b, a, _ := controlFixture(t)
	b.run = func(_ context.Context, op string, in, out any) error {
		if op != "observe" {
			t.Fatalf("operation=%s", op)
		}
		*out.(*Observation) = Observation{Window: c.windows["owned"].window, Width: 10, Height: 10, PIDInputCertificationConfigured: true}
		return nil
	}
	got, err := c.Observe(t.Context(), a, "owned", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.PIDInputCertificationConfigured {
		t.Fatal("native reply invented PID certification")
	}
}
