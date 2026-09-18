package appcontrol

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func adoptionFixture(t *testing.T) (*Controller, *controlledBackend, HumanRequest, Window) {
	t.Helper()
	c, b, a, _ := controlFixture(t)
	d := c.windows["owned"].display
	w := c.windows["owned"].window
	delete(c.windows, "owned")
	w.Handle = "candidate"
	w.Process = Process{PID: os.Getpid(), UID: uint32(os.Getuid()), Start: t.Name(), BundleID: "org.example.Editor", OSBuild: "test"}
	w.Bounds = Bounds{10, 10, 500, 400}
	w.DisplayID = 1
	c.config.AuthorizeHuman = func(_ context.Context, r HumanRequest) (Display, error) {
		if r.Grant != "local-owner" || r.Resource != a.Resource {
			return Display{}, refusal("human_grant_required")
		}
		return d, nil
	}
	b.run = func(_ context.Context, op string, in, out any) error {
		switch op {
		case "list_windows":
			*out.(*nativeCandidates) = nativeCandidates{Windows: []nativeCandidate{{Window: w, Title: "Synthetic"}}}
		case "adopt_window":
			*out.(*Window) = in.(map[string]any)["Window"].(Window)
		}
		return nil
	}
	t.Cleanup(func() {
		if err := c.Dispose(context.Background(), a.Resource); err != nil {
			t.Error(err)
		}
	})
	return c, b, HumanRequest{Grant: "local-owner", Resource: a.Resource, InterventionID: "intervention", Direction: "list_existing"}, w
}

func TestLocalAppSelectionBoundedOneUseAndNoMovement(t *testing.T) {
	c, b, r, w := adoptionFixture(t)
	listed, err := c.ListWindows(t.Context(), r)
	if err != nil || len(listed.Windows) != 1 || listed.Windows[0].Handle != w.Handle {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	r.Direction = "adopt_existing"
	r.WindowHandle = w.Handle
	got, err := c.AdoptWindow(t.Context(), r)
	if err != nil || got.Handle != w.Handle || !c.windows[w.Handle].human {
		t.Fatalf("window=%+v err=%v", got, err)
	}
	if _, err = c.AdoptWindow(t.Context(), r); err == nil {
		t.Fatal("candidate replay accepted")
	}
	for _, op := range b.calls {
		if op == "move" || op == "action" {
			t.Fatalf("selection performed %s", op)
		}
	}
}

func TestLocalAppSelectionRejectsScopeExpiryAndDisappearedWindow(t *testing.T) {
	for _, tc := range []string{"foreign", "intervention", "expired", "disappeared", "claimed", "geometry"} {
		t.Run(tc, func(t *testing.T) {
			c, b, r, w := adoptionFixture(t)
			if _, err := c.ListWindows(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			r.Direction = "adopt_existing"
			r.WindowHandle = w.Handle
			switch tc {
			case "foreign":
				r.Resource.RuntimeID = "foreign"
			case "intervention":
				r.InterventionID = "other"
			case "expired":
				v := c.candidates[w.Handle]
				v.expires = time.Now().Add(-time.Second)
				c.candidates[w.Handle] = v
			case "disappeared":
				b.run = func(_ context.Context, op string, in, out any) error {
					if op == "adopt_window" {
						return refusal("stale_window")
					}
					return nil
				}
			case "geometry":
				b.run = func(_ context.Context, op string, in, out any) error {
					if op == "adopt_window" {
						v := w
						v.Bounds.X++
						*out.(*Window) = v
					}
					return nil
				}
			case "claimed":
				claim, err := nativeClaim(w.Process)
				if err != nil {
					t.Fatal(err)
				}
				defer claim.ReleaseAfterQuiescence()
			}
			if _, err := c.AdoptWindow(t.Context(), r); err == nil {
				t.Fatal("unsafe selection accepted")
			}
		})
	}
}

func TestLocalAppCandidatesExcludeOwnedAmbiguousAndUnreadable(t *testing.T) {
	for _, tc := range []string{"claimed", "ambiguous", "unreadable"} {
		t.Run(tc, func(t *testing.T) {
			c, b, r, w := adoptionFixture(t)
			if tc == "claimed" {
				claim, err := nativeClaim(w.Process)
				if err != nil {
					t.Fatal(err)
				}
				defer claim.ReleaseAfterQuiescence()
			}
			if tc == "ambiguous" {
				b.run = func(_ context.Context, op string, in, out any) error {
					if op == "list_windows" {
						other := w
						other.Handle = "second"
						*out.(*nativeCandidates) = nativeCandidates{Windows: []nativeCandidate{{Window: w}, {Window: other}}}
					}
					return nil
				}
			}
			if tc == "unreadable" {
				b.run = func(_ context.Context, op string, in, out any) error {
					if op == "list_windows" {
						return errors.New("unavailable")
					}
					return nil
				}
			}
			got, err := c.ListWindows(t.Context(), r)
			if tc == "unreadable" {
				if err == nil {
					t.Fatal("unreadable inventory succeeded")
				}
			} else if err != nil || len(got.Windows) != 0 {
				t.Fatalf("unsafe candidates=%+v err=%v", got, err)
			}
		})
	}
}
