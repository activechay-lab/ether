package plex

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// recordActivities keeps only actual in-flight tasks. Completion removes a row
// before sending the ended frame so a client refetch never sees a stale spinner.
func (s *Server) recordActivities(nc model.NotificationContainer) {
	if len(nc.ActivityNotification) == 0 {
		return
	}
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	for _, event := range nc.ActivityNotification {
		if event.Event == "ended" {
			delete(s.activities, event.UUID)
			continue
		}
		if event.Activity != nil && (event.Event == "started" || event.Event == "updated") {
			a := *event.Activity
			a.UUID = event.UUID
			if s.activities == nil {
				s.activities = make(map[string]model.Activity)
			}
			s.activities[event.UUID] = a
		}
	}
}

func (s *Server) handleActivities(w http.ResponseWriter, r *http.Request) {
	pr := FromContext(r.Context())
	s.activityMu.Lock()
	rows := make([]model.Activity, 0, len(s.activities))
	for _, a := range s.activities {
		if pr.User != nil && (pr.User.IsOwner || a.UserID == pr.User.ID) {
			a.Title = activityTitle(a.Title, notificationLanguage(r))
			rows = append(rows, a)
		}
	}
	s.activityMu.Unlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].UUID < rows[j].UUID })
	s.write(w, r, &model.MediaContainer{Size: len(rows), Activity: rows})
}

func (s *Server) handleActivityDelete(w http.ResponseWriter, r *http.Request) {
	// None of Ether's metadata/subtitle tasks is cancellable through PMS.
	writeHTTPError(w, http.StatusNotFound)
}

func (s *Server) handleUpdaterCheck(w http.ResponseWriter, r *http.Request) {
	// Ether is upgraded by its native manager, not the PMS binary updater. The
	// measured unavailable/manual-install result resolves Plex Web's check UI.
	s.publishNotification(model.NotificationContainer{Type: "update.statechange", Size: 1,
		AutoUpdateNotification: []model.AutoUpdateNotification{{State: "error", CanInstall: false}},
	}, FromContext(r.Context()).User.ID)
	w.WriteHeader(http.StatusOK)
}

// notificationLanguage is captured when a socket opens. The explicit Plex
// language wins; browser locale negotiation provides the ordinary Web fallback.
func notificationLanguage(r *http.Request) string {
	if language := FromContext(r.Context()).Language; language != "" {
		return language
	}
	best, quality, bestRank := "", -1.0, -1
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		q := 1.0
		for _, field := range fields[1:] {
			if raw, ok := strings.CutPrefix(strings.TrimSpace(field), "q="); ok {
				if value, err := strconv.ParseFloat(raw, 64); err == nil && value >= 0 && value <= 1 {
					q = value
				} else {
					q = 0
				}
			}
		}
		tag := strings.TrimSpace(fields[0])
		base := strings.ToLower(strings.Split(strings.ReplaceAll(tag, "_", "-"), "-")[0])
		exact := strings.Contains("|af|ar|bg|cs|da|de|el|en|es|et|fa|fi|fr|he|hr|hu|id|is|it|ja|ko|lt|lv|ms|nb|nl|no|pl|pt|ro|ru|sk|sr|sv|th|tr|uk|vi|zh|", "|"+base+"|") && !traditionalChinese(tag)
		rank := 0
		if base == "ca" || base == "nn" {
			rank = 1
		}
		if exact {
			rank = 2
		}
		if q > 0 && (rank > bestRank || (rank == bestRank && q > quality)) {
			best, quality, bestRank = tag, q, rank
		}
	}
	return best
}

func localizeNotification(msg notification, language string) notification {
	if msg.typ != "activity" || language == "" {
		return msg
	}
	var root struct {
		NotificationContainer struct {
			ActivityNotification []struct{ Activity map[string]json.RawMessage }
		}
	}
	if json.Unmarshal(msg.data, &root) != nil {
		return msg
	}
	// Preserve field order and unrelated data: replace only quoted title values.
	data := string(msg.data)
	for _, event := range root.NotificationContainer.ActivityNotification {
		raw := event.Activity["title"]
		var title string
		if json.Unmarshal(raw, &title) != nil {
			continue
		}
		localized := activityTitle(title, language)
		if localized == title {
			continue
		}
		encoded, _ := json.Marshal(localized)
		data = strings.ReplaceAll(data, `"title":`+string(raw), `"title":`+string(encoded))
	}
	if data == string(msg.data) {
		return msg
	}
	return newNotification(msg.typ, []byte(data))
}

func activityTitle(title, language string) string {
	if traditionalChinese(language) {
		return title
	}
	language = strings.ToLower(strings.Split(strings.ReplaceAll(language, "_", "-"), "-")[0])
	if language == "ca" {
		language = "es"
	}
	if language == "nn" {
		language = "no"
	}
	if value := activityTranslations[title][language]; value != "" {
		return value
	}
	return title
}

var activityTranslations = map[string]map[string]string{
	"Downloading subtitles": {"de": "Untertitel werden heruntergeladen", "es": "Descargando subtítulos", "fr": "Téléchargement des sous-titres", "it": "Download dei sottotitoli", "pt": "Baixando legendas", "nl": "Ondertitels downloaden", "ja": "字幕をダウンロード中", "ko": "자막 다운로드 중", "zh": "正在下载字幕", "ru": "Загрузка субтитров", "pl": "Pobieranie napisów", "sv": "Hämtar undertexter", "da": "Henter undertekster", "no": "Laster ned undertekster", "nb": "Laster ned undertekster", "fi": "Ladataan tekstityksiä", "tr": "Altyazılar indiriliyor", "cs": "Stahování titulků", "hu": "Feliratok letöltése", "uk": "Завантаження субтитрів", "ar": "تنزيل الترجمة", "af": "Laai onderskrifte af", "bg": "Изтегляне на субтитри", "el": "Λήψη υποτίτλων", "fa": "در حال دانلود زیرنویس", "he": "מוריד כתוביות", "hr": "Preuzimanje titlova", "id": "Mengunduh subtitle", "is": "Sæki skjátexta", "ro": "Se descarcă subtitrările", "sk": "Sťahovanie titulkov", "th": "กำลังดาวน์โหลดคำบรรยาย", "vi": "Đang tải phụ đề", "ms": "Memuat turun sari kata", "sr": "Преузимање титлова", "et": "Subtiitrite allalaadimine", "lt": "Atsisiunčiami subtitrai", "lv": "Subtitru lejupielāde"},
}

func traditionalChinese(tag string) bool {
	tag = strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
	return strings.HasPrefix(tag, "zh-hant") || tag == "zh-tw" || tag == "zh-hk" || tag == "zh-mo"
}

// Ether's catalog has no PMS scanner, agent or butler job behind these writes.
// Refuse them rather than acknowledging a task that will never produce events.
func (s *Server) handleUnsupportedManagement(w http.ResponseWriter, r *http.Request) {
	writeHTTPError(w, http.StatusNotFound)
}
