package service

import ()

// newWithBackend is the test seam.
func newWithBackend(run RunFunc, opts Options, be backend) (*Service, error) {
	opts2, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	cfg, err := opts2.Config()
	if err != nil {
		return nil, err
	}
	return &Service{
		opts: opts2, cfg: cfg,
		prog: &program{run: run, log: opts2.Logger, stopTimeout: opts2.StopTimeout},
		svc:  be, log: opts2.Logger,
	}, nil
}
