//go:build windows

package service

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

// IsWindowsService reports whether the process was started by the Windows
// Service Control Manager.
func IsWindowsService() bool {
	serviceProcess, err := svc.IsWindowsService()
	return err == nil && serviceProcess
}

// RunWindowsService runs the application callback under the Windows Service
// Control Manager and cancels it on stop or shutdown requests.
func RunWindowsService(run func(context.Context) error) error {
	if run == nil {
		return nil
	}
	return svc.Run(appName, windowsServiceHandler{run: run})
}

type windowsServiceHandler struct {
	run func(context.Context) error
}

func (handler windowsServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- handler.run(ctx) }()
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			statuses <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		case request, ok := <-requests:
			if !ok {
				cancel()
				err := <-done
				statuses <- svc.Status{State: svc.Stopped}
				if err != nil {
					return false, 1
				}
				return false, 0
			}
			if request.Cmd != svc.Stop && request.Cmd != svc.Shutdown {
				continue
			}
			statuses <- svc.Status{State: svc.StopPending}
			cancel()
			err := <-done
			statuses <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
