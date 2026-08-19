//go:build !linux

package observability

import "errors"

const resourceSamplingSupported = false

var errUnsupported = errors.New("resource sampling unsupported on this platform")

func processCPUSeconds() (float64, error)      { return 0, errUnsupported }
func processRSSBytes() (float64, error)        { return 0, errUnsupported }
func diskUsedFraction(string) (float64, error) { return 0, errUnsupported }
