package daemon

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func responseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > 0 && seconds <= int64((24*time.Hour)/time.Second) {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return min(deadline.Sub(now), 24*time.Hour)
	}
	return 0
}

func reportRetryDelay(attempt int, err error) time.Duration {
	delay := downwardJitterDuration(min(5*time.Second*time.Duration(1<<min(max(attempt-1, 0), 6)), 5*time.Minute))
	var response *requestError
	if errors.As(err, &response) {
		delay = max(delay, response.RetryAfter)
	}
	return delay
}
