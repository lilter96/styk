package doctor

import (
	"context"
)

// Probe is injected only on explicit live opt-in. Errors are never serialized.
type Probe func(context.Context) (bool, error)

func Live(ctx context.Context, r *Report, moy, ozon Probe) {
	for _, p := range []struct {
		code string
		run  Probe
	}{{"MOYSKLAD_CONNECTION", moy}, {"OZON_CONNECTION", ozon}} {
		for i := range r.Checks {
			c := &r.Checks[i]
			if c.Code != p.code {
				continue
			}
			if p.run == nil {
				c.Status = NotChecked
				c.Detail = "Registration or matching local credentials are unavailable; no request performed."
				continue
			}
			verified, err := p.run(ctx)
			if err != nil {
				c.Status = Fail
				c.Detail = "Bounded read failed; inspect credentials, account scope or transport availability without logging responses."
			} else if !verified {
				c.Status = Warn
				c.Detail = "Read succeeded, but the response did not independently verify account identity."
			} else {
				c.Status = Pass
				c.Detail = "A bounded read succeeded for the selected credentials; synchronization correctness and full permissions remain unverified."
			}
		}
	}
}
