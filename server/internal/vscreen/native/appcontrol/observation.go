package appcontrol

import "context"

// Observe rechecks window/process/display identity and returns no fallback screen.
func (c *Controller) Observe(ctx context.Context, a Authority, handle string, png bool) (Observation, error) {
	ctx, leave, err := c.enter(ctx, a.Resource)
	if err != nil {
		return Observation{}, err
	}
	defer leave()
	d, err := c.authorize(ctx, a, ObserveAccess)
	if err != nil {
		return Observation{}, err
	}
	if handle == "" {
		var observation Observation
		err = c.backend.call(ctx, "observe_display", map[string]any{"Display": d, "PNG": png}, &observation)
		if err != nil {
			return Observation{}, err
		}
		c.reportPIDPolicy(ctx, &observation)
		observation.Display = d
		return observation, nil
	}
	w, err := c.owned(handle, d)
	if err != nil {
		return Observation{}, err
	}
	var observation Observation
	err = c.backend.call(ctx, "observe", map[string]any{"Window": w.window, "Display": d, "PNG": png}, &observation)
	if err != nil {
		c.freeze(a.Resource)
		return Observation{}, err
	}
	if observation.Window.Handle != handle || observation.Window.Process != w.window.Process || observation.Window.WindowID != w.window.WindowID || observation.Window.DisplayID != d.ID || observation.Window.SnapshotRevision == 0 || observation.Width == 0 || observation.Height == 0 || !d.Bounds.Contains(observation.Window.Bounds) {
		c.freeze(a.Resource)
		return Observation{}, refusal("stale_window")
	}
	observation.Display = d
	c.reportPIDPolicy(ctx, &observation)
	w.window = observation.Window
	return observation, nil
}

func (c *Controller) reportPIDPolicy(ctx context.Context, o *Observation) {
	o.PIDInputCertificationConfigured = c.config.CertifiedPIDInput != nil
	o.PIDInputVerification = "none"
	o.PIDInputCompletionAvailable = c.config.VerifyPIDCompletion != nil
	if o.PIDInputCompletionAvailable && o.Window.Handle != "" && c.config.PIDInputVerification != nil && c.config.PIDInputVerification(c.pidIdentity(ctx, o.Window.Process)) == "verified_variants" {
		o.PIDInputVerification = "verified_variants"
	}
}
