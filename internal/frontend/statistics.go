package frontend

import (
	"github.com/braedonsaunders/ether/internal/hoststat"
)

// Statistics values are snapshots. Adapters parse protocol filters and render
// these values; the host owns device identity, aggregation and retention.
type ResourceSample = hoststat.Sample

const (
	StatisticsYear   = hoststat.SpanYear
	StatisticsMonth  = hoststat.SpanMonth
	StatisticsWeek   = hoststat.SpanWeek
	StatisticsDay    = hoststat.SpanDay
	StatisticsHour   = hoststat.SpanHour
	StatisticsSecond = hoststat.SpanSec
)

type Dashboard interface {
	SeeDevice(clientID, name, platform string) StatisticsDevice
	Devices() []StatisticsDevice
	Device(id int64) (StatisticsDevice, bool)
	Bandwidth(StatisticsQuery) StatisticsPage
	Media(StatisticsQuery) StatisticsPage
	RecordBytes(accountID int64, accountName, clientID, deviceName, platform string, lan bool, n int64)
	RecordPlay(accountID int64, accountName, clientID string, metadataType int, durationSec int64)
}

type ResourceStatistics interface {
	Query(span int, atMin, atMax, exactAt int64, hasExact, hasMin, hasMax bool) []ResourceSample
}

// StatisticsDevice is one client the Dashboard can name.
type StatisticsDevice struct {
	ID               int64
	ClientIdentifier string
	Name             string
	Platform         string
	CreatedAt        int64
}

// StatisticsBandwidthRow is one StatisticsBandwidth series point.
type StatisticsBandwidthRow struct {
	AccountID int64
	DeviceID  int64
	Timespan  int
	At        int64
	Lan       int
	Bytes     int64
}

// StatisticsMediaRow is one StatisticsMedia series point.
type StatisticsMediaRow struct {
	AccountID    int64
	DeviceID     int64
	Timespan     int
	At           int64
	MetadataType int
	Count        int64
	Duration     int64 // sum of item lengths in seconds
}

// StatisticsQuery is the parsed Dashboard filter. Timespans empty means "every kept
// rollup except per-second" for bandwidth/media (0..4).
type StatisticsQuery struct {
	Timespans                     []int
	AccountID                     int64
	DeviceID                      int64
	HasAccount, HasDevice, HasLan bool
	Lan                           int
	AtMin, AtMax                  int64
	ExactAt                       int64
	HasExact, HasAtMin, HasAtMax  bool
	Offset                        int
	Limit                         int
	HasLimit                      bool
	Sort                          string
	Desc                          bool
}

// StatisticsPage is one statistics answer: the series window and the StatisticsDevice/Account
// lookup for ids that window names.
type StatisticsPage struct {
	Rows      []StatisticsBandwidthRow
	Media     []StatisticsMediaRow
	Devices   []StatisticsDevice
	Accounts  []StatisticsAccountRef
	TotalSize int
}

// StatisticsAccountRef labels an account id on a statistics page.
type StatisticsAccountRef struct {
	ID   int64
	Name string
}
