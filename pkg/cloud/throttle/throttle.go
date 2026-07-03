package throttle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	alierrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

// Class categorizes an error returned by an OpenAPI call for throttling purposes.
type Class int

const (
	// ClassUnknown means the error is not a recognized cloud API error, so we
	// cannot tell whether we are being throttled. Throttle state is left untouched.
	ClassUnknown Class = iota
	// ClassOK means the call was a recognized outcome that is not throttling
	// (success or a non-throttling cloud error). Any active throttling is cleared.
	ClassOK
	// ClassThrottling means the cloud responded with a throttling error, so we
	// should back off and retry.
	ClassThrottling
)

// Classifier maps an error (which may be nil) to a Class plus a best-effort
// request ID for logging (empty when unavailable). It lets the Throttler work
// with different Alibaba Cloud SDKs whose error types are incompatible.
type Classifier func(error) (Class, string)

// V1Classifier classifies errors from the old SDK
// (github.com/aliyun/alibaba-cloud-sdk-go), whose errors are *alierrors.ServerError.
func V1Classifier(err error) (Class, string) {
	if err == nil {
		return ClassOK, ""
	}
	alierr, ok := errors.AsType[*alierrors.ServerError](err)
	if !ok {
		return ClassUnknown, ""
	}
	if alierr.ErrorCode() == "Throttling" {
		return ClassThrottling, alierr.RequestId()
	}
	return ClassOK, alierr.RequestId()
}

// V2Classifier classifies errors from the new tea SDK
// (github.com/alibabacloud-go/*), whose errors are *tea.SDKError. Throttling
// codes take several forms (Throttling, Throttling.User, Throttling.Api), so we
// match on the "Throttling" prefix.
func V2Classifier(err error) (Class, string) {
	if err == nil {
		return ClassOK, ""
	}
	sdkErr, ok := errors.AsType[*tea.SDKError](err)
	if !ok {
		return ClassUnknown, ""
	}
	requestID := sdkErrorRequestID(sdkErr)
	if strings.HasPrefix(tea.StringValue(sdkErr.Code), "Throttling") {
		return ClassThrottling, requestID
	}
	return ClassOK, requestID
}

// sdkErrorRequestID extracts the request ID from a tea SDKError. The tea SDK
// stashes it inside the JSON-encoded Data field rather than a typed accessor
// (see pkg/cloud/wrap.transformV2ErrorForLog).
func sdkErrorRequestID(e *tea.SDKError) string {
	if e.Data == nil {
		return ""
	}
	data := *e.Data
	if data == "" || data[0] != '{' { // only attempt to parse a JSON object
		return ""
	}
	var parsed struct {
		RequestId string
	}
	if json.Unmarshal([]byte(data), &parsed) != nil {
		return ""
	}
	return parsed.RequestId
}

type throttlingError struct {
	err error
}

func (e throttlingError) Error() string {
	return fmt.Sprintf("OpenAPI is throttling: %v", e.err)
}

func (t throttlingError) Unwrap() error {
	return t.err
}

type throttlingContext struct {
	probing chan struct{}

	lastThrottling time.Time
	retryDelay     time.Duration
}

type Throttler struct {
	initDelay time.Duration
	maxDelay  time.Duration

	clk      clock.Clock
	classify Classifier

	current atomic.Pointer[throttlingContext]
}

func NewThrottler(clk clock.Clock, initDelay, maxDelay time.Duration, classify Classifier) *Throttler {
	return &Throttler{
		initDelay: initDelay,
		maxDelay:  maxDelay,
		clk:       clk,
		classify:  classify,
	}
}

// Throttle will call f and retry after delay when Throttling error code is returned.
// If one Throttling error is observed, all subsequent requests will be delayed.
// A random delayed request will be used to probe whether the throttling has ended.
// If a non-Throttling response is returned, all delayed requests will be sent out immediately.
//
// This works with Alibaba Cloud OpenAPI, which usually enforce a quota every minute.
// If we use up the quota in the first 10s, we will get Throttling error code in the following 50s.
func (t *Throttler) Throttle(ctx context.Context, f func() error) error {
	logger := klog.FromContext(ctx)
	start := t.clk.Now()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		tCtx := t.current.Load()
		if tCtx != nil {
			logger.V(3).Info("OpenAPI throttling in effect")
			select {
			case <-ctx.Done():
				return throttlingError{ctx.Err()}
			case _, ok := <-tCtx.probing:
				if ok {
					// Currently throttling, we will probe if the throttling has ended.
					// Be sure to write or close tCtx.probing to notify others.
					select {
					case <-ctx.Done():
						tCtx.probing <- struct{}{}
						return throttlingError{ctx.Err()}
					case <-t.clk.After(tCtx.retryDelay - t.clk.Since(tCtx.lastThrottling)):
					}
					logger.V(2).Info("OpenAPI throttling, probing", "retry", tCtx.retryDelay)
				} else {
					logger.V(2).Info("OpenAPI no longer throttle", "delay", t.clk.Since(start))
					tCtx = nil
				}
			}
		}

		err := f()

		class, requestID := t.classify(err)
		switch class {
		case ClassUnknown:
			// We are not sure what's wrong, don't change state.
			if tCtx != nil {
				// Initiate the next probe immediately.
				tCtx.probing <- struct{}{}
			}
			return err
		case ClassOK:
			if tCtx != nil {
				logger.V(1).Info("OpenAPI throttling ended")
				close(tCtx.probing)
				t.current.CompareAndSwap(tCtx, nil)
			}
			return err
		}

		// Throttling detected
		if tCtx == nil {
			tCtx = &throttlingContext{
				lastThrottling: t.clk.Now(),
				retryDelay:     t.initDelay,
				probing:        make(chan struct{}, 1),
			}
			tCtx.probing <- struct{}{}
			if t.current.CompareAndSwap(nil, tCtx) {
				logger.V(1).Info("OpenAPI throttling detected", "requestID", requestID)
			} else {
				logger.V(3).Info("already throttling", "requestID", requestID)
			}
		} else {
			tCtx.lastThrottling = t.clk.Now()
			tCtx.retryDelay *= 2
			if tCtx.retryDelay > t.maxDelay {
				tCtx.retryDelay = t.maxDelay
			}
			tCtx.probing <- struct{}{}
			logger.V(2).Info("still throttling", "retry", tCtx.retryDelay, "requestID", requestID)
		}
	}
}

func Throttled[TReq any, TResp any](t *Throttler, f func(TReq) (TResp, error)) func(context.Context, TReq) (TResp, error) {
	return func(ctx context.Context, req TReq) (TResp, error) {
		var resp TResp
		err := t.Throttle(ctx, func() (err error) {
			resp, err = f(req)
			return err
		})
		return resp, err
	}
}
