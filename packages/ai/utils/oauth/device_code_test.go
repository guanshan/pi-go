package oauth

import (
	"context"
	"testing"
	"time"
)

// fakeClock drives PollDeviceCodeFlow deterministically: Now() advances by the
// requested sleep duration so the deadline logic is exercised without real time.
type fakeClock struct {
	now    time.Time
	slept  []time.Duration
	cancel context.CancelFunc
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.slept = append(f.slept, d)
	f.now = f.now.Add(d)
	return nil
}

func TestPollDeviceCodeFlowSucceeds(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	calls := 0
	token, err := PollDeviceCodeFlow(context.Background(), DeviceCodePollOptions{
		IntervalSeconds:  1,
		ExpiresInSeconds: 60,
		Now:              clock.Now,
		Sleep:            clock.Sleep,
		Poll: func(context.Context) (DeviceCodePollResult, error) {
			calls++
			if calls < 3 {
				return DeviceCodePollResult{Status: DeviceCodePending}, nil
			}
			return DeviceCodePollResult{Status: DeviceCodeComplete, AccessToken: "tok-123"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok-123" {
		t.Fatalf("token=%q, want tok-123", token)
	}
	if calls != 3 {
		t.Fatalf("poll calls=%d, want 3", calls)
	}
}

func TestPollDeviceCodeFlowExpires(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	_, err := PollDeviceCodeFlow(context.Background(), DeviceCodePollOptions{
		IntervalSeconds:  5,
		ExpiresInSeconds: 7, // one poll, then the deadline passes during the wait
		Now:              clock.Now,
		Sleep:            clock.Sleep,
		Poll: func(context.Context) (DeviceCodePollResult, error) {
			return DeviceCodePollResult{Status: DeviceCodePending}, nil
		},
	})
	if err == nil || err.Error() != deviceFlowTimeoutMessage {
		t.Fatalf("err=%v, want %q", err, deviceFlowTimeoutMessage)
	}
}

func TestPollDeviceCodeFlowSlowDownTimeout(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	_, err := PollDeviceCodeFlow(context.Background(), DeviceCodePollOptions{
		IntervalSeconds:  1,
		ExpiresInSeconds: 12,
		Now:              clock.Now,
		Sleep:            clock.Sleep,
		Poll: func(context.Context) (DeviceCodePollResult, error) {
			return DeviceCodePollResult{Status: DeviceCodeSlowDown}, nil
		},
	})
	if err == nil || err.Error() != deviceFlowSlowDownTimeout {
		t.Fatalf("err=%v, want slow-down timeout message", err)
	}
}

func TestPollDeviceCodeFlowFailedPropagatesMessage(t *testing.T) {
	_, err := PollDeviceCodeFlow(context.Background(), DeviceCodePollOptions{
		IntervalSeconds:  1,
		ExpiresInSeconds: 60,
		Poll: func(context.Context) (DeviceCodePollResult, error) {
			return DeviceCodePollResult{Status: DeviceCodeFailed, Message: "access_denied"}, nil
		},
	})
	if err == nil || err.Error() != "access_denied" {
		t.Fatalf("err=%v, want access_denied", err)
	}
}

func TestPollDeviceCodeFlowContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clock := &fakeClock{now: time.Unix(0, 0)}
	_, err := PollDeviceCodeFlow(ctx, DeviceCodePollOptions{
		IntervalSeconds:  1,
		ExpiresInSeconds: 60,
		Now:              clock.Now,
		Sleep:            clock.Sleep,
		Poll: func(context.Context) (DeviceCodePollResult, error) {
			cancel() // cancel after the first poll; the next loop iteration sees ctx.Err
			return DeviceCodePollResult{Status: DeviceCodePending}, nil
		},
	})
	if err == nil || err.Error() != cancelMessage {
		t.Fatalf("err=%v, want %q", err, cancelMessage)
	}
}

func TestPollDeviceCodeFlowRequiresPoll(t *testing.T) {
	_, err := PollDeviceCodeFlow(context.Background(), DeviceCodePollOptions{})
	if err == nil {
		t.Fatal("expected error when Poll is nil")
	}
}

func TestDurationFromSecondsInfinite(t *testing.T) {
	if d := DurationFromSeconds(2); d != 2*time.Second {
		t.Fatalf("DurationFromSeconds(2)=%v", d)
	}
}
