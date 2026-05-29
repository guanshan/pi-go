package oauth

import (
	"context"
	"fmt"
	"math"
	"time"
)

const (
	cancelMessage              = "Login cancelled"
	deviceFlowTimeoutMessage   = "Device flow timed out"
	deviceFlowSlowDownTimeout  = "Device flow timed out after one or more slow_down responses. This is often caused by clock drift in WSL or VM environments. Please sync or restart the VM clock and try again."
	minimumPollInterval        = time.Second
	defaultPollIntervalSeconds = 5
	slowDownIntervalIncrement  = 5 * time.Second
)

type deviceCodeFlowError string

func (e deviceCodeFlowError) Error() string { return string(e) }

type DeviceCodePollStatus string

const (
	DeviceCodePending  DeviceCodePollStatus = "pending"
	DeviceCodeSlowDown DeviceCodePollStatus = "slow_down"
	DeviceCodeComplete DeviceCodePollStatus = "complete"
	DeviceCodeFailed   DeviceCodePollStatus = "failed"
)

type DeviceCodePollResult struct {
	Status      DeviceCodePollStatus
	AccessToken string
	Message     string
}

type DeviceCodePollOptions struct {
	IntervalSeconds  float64
	ExpiresInSeconds float64
	Poll             func(context.Context) (DeviceCodePollResult, error)
	Sleep            func(context.Context, time.Duration) error
	Now              func() time.Time
}

func PollDeviceCodeFlow(ctx context.Context, options DeviceCodePollOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Poll == nil {
		return "", deviceCodeFlowError("OAuth device code poll function is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = SleepContext
	}
	var deadline time.Time
	if options.ExpiresInSeconds > 0 {
		deadline = now().Add(DurationFromSeconds(options.ExpiresInSeconds))
	}
	intervalSeconds := options.IntervalSeconds
	if intervalSeconds <= 0 {
		intervalSeconds = defaultPollIntervalSeconds
	}
	interval := DurationFromSeconds(intervalSeconds)
	if interval < minimumPollInterval {
		interval = minimumPollInterval
	}
	slowDownResponses := 0
	for deadline.IsZero() || now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return "", deviceCodeFlowError(cancelMessage)
		}
		result, err := options.Poll(ctx)
		if err != nil {
			return "", err
		}
		switch result.Status {
		case DeviceCodeComplete:
			return result.AccessToken, nil
		case DeviceCodePending:
			// Wait below before polling again.
		case DeviceCodeSlowDown:
			slowDownResponses++
			interval += slowDownIntervalIncrement
			if interval < minimumPollInterval {
				interval = minimumPollInterval
			}
		case DeviceCodeFailed:
			if result.Message == "" {
				return "", deviceCodeFlowError("device flow failed")
			}
			return "", deviceCodeFlowError(result.Message)
		default:
			return "", fmt.Errorf("unknown device code poll status: %s", result.Status)
		}
		wait := interval
		if !deadline.IsZero() {
			remaining := deadline.Sub(now())
			if remaining <= 0 {
				break
			}
			if remaining < wait {
				wait = remaining
			}
		}
		if wait > 0 {
			if err := sleep(ctx, wait); err != nil {
				return "", deviceCodeFlowError(cancelMessage)
			}
		}
	}
	if slowDownResponses > 0 {
		return "", deviceCodeFlowError(deviceFlowSlowDownTimeout)
	}
	return "", deviceCodeFlowError(deviceFlowTimeoutMessage)
}

func SleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func DurationFromSeconds(seconds float64) time.Duration {
	if math.IsInf(seconds, 0) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds * float64(time.Second))
}
