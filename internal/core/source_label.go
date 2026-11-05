package core

func SourceLabel(s Source) string {
	switch s {
	case SourceRemux:
		return "Remux"
	case SourceWEBDL:
		return "WEB-DL"
	case SourceBluRay:
		return "Blu-ray"
	case SourceWEBRip:
		return "WEBRip"
	case SourceHDTV:
		return "HDTV"
	case SourceDVD:
		return "DVD"
	case SourceScreener:
		return "Screener"
	case SourceCam:
		return "CAM"
	}
	return ""
}
