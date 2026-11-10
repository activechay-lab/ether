package livetv

// GuideSnapshotBuilds counts virtual guide snapshot builds (tests only).
func (s *Service) GuideSnapshotBuilds() int64 { return s.virtualGuide.builds.Load() }
