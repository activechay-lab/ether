//go:build !linux && !darwin && !windows

package hoststat

func (s *nativeNodeSampler) platformSample() NodeReading { return NodeReading{} }
