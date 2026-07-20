package collector

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/model"
)

var validGranularities = map[string]bool{
	"day": true, "month": true, "quarter": true, "year": true,
}

func (t *Tracker) ensureMonth(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureMonthLocked(now)
}

func (t *Tracker) ensureMonthLocked(now time.Time) {
	local := now.In(time.Local)
	period := local.Format("2006-01")
	if t.currentMonth == "" {
		t.currentMonth = period
		return
	}
	if t.currentMonth == period {
		return
	}
	resetAt := time.Date(
		local.Year(), local.Month(), 1, 0, 0, 0, 0, time.Local,
	).UTC()
	for key, host := range t.hosts {
		host.Uploaded = 0
		host.Downloaded = 0
		host.CounterResetAt = resetAt
		t.hosts[key] = host
	}
	t.currentMonth = period
}

func monthBounds(period string) (time.Time, time.Time) {
	value, err := time.ParseInLocation("2006-01", period, time.Local)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	return value.UTC(), value.AddDate(0, 1, 0).UTC()
}

func (t *Tracker) recordUsageLocked(
	now time.Time, hostID string, uploaded, downloaded uint64,
) {
	if uploaded == 0 && downloaded == 0 {
		return
	}
	hostID = t.canonicalIDLocked(hostID)
	day := now.In(time.Local).Format("2006-01-02")
	if t.daily[day] == nil {
		t.daily[day] = make(map[string]model.TrafficUsage)
	}
	usage := t.daily[day][hostID]
	usage.Uploaded += uploaded
	usage.Downloaded += downloaded
	t.daily[day][hostID] = usage
}

func (t *Tracker) UsageHistory(
	granularity, period string,
) (model.UsageHistory, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !validGranularities[granularity] {
		return model.UsageHistory{}, errors.New("invalid history granularity")
	}
	options := t.periodOptionsLocked(granularity)
	if period == "" && len(options) > 0 {
		period = options[0]
	}
	if !validPeriod(granularity, period) {
		return model.UsageHistory{}, errors.New("invalid history period")
	}

	aggregated := make(map[string]model.TrafficUsage)
	for day, hosts := range t.daily {
		if !periodContains(granularity, period, day) {
			continue
		}
		for id, usage := range hosts {
			id = t.canonicalIDLocked(id)
			total := aggregated[id]
			total.Uploaded += usage.Uploaded
			total.Downloaded += usage.Downloaded
			aggregated[id] = total
		}
	}

	records := make([]model.UsageRecord, 0, len(aggregated))
	var totals model.TrafficUsage
	for id, usage := range aggregated {
		profile := t.profiles[id]
		records = append(records, model.UsageRecord{
			HostID: id, Hostname: profile.Hostname, MAC: profile.MAC,
			Addresses: append([]model.HostAddress(nil), profile.Addresses...),
			Uploaded:  usage.Uploaded, Downloaded: usage.Downloaded,
		})
		totals.Uploaded += usage.Uploaded
		totals.Downloaded += usage.Downloaded
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := primaryProfileIP(records[i]), primaryProfileIP(records[j])
		if comparison := compareHostAddress(left, right); comparison != 0 {
			return comparison < 0
		}
		return records[i].HostID < records[j].HostID
	})
	return model.UsageHistory{
		Granularity: granularity,
		Period:      period,
		Options:     options,
		Records:     records,
		Totals:      totals,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

func (t *Tracker) ExportUsageTXT(
	granularity, period string,
) (string, string, error) {
	history, err := t.UsageHistory(granularity, period)
	if err != nil {
		return "", "", err
	}
	var output strings.Builder
	output.WriteString("\uFEFFPeriod\tHost\tMAC\tIPv4\tIPv6\tDownloaded bytes\tUploaded bytes\tDownloaded\tUploaded\n")
	for _, record := range history.Records {
		var ipv4, ipv6 []string
		for _, addr := range record.Addresses {
			if addr.Family == "ipv4" {
				ipv4 = append(ipv4, addr.IP)
			} else {
				ipv6 = append(ipv6, addr.IP+" ("+addr.Scope+")")
			}
		}
		name := record.Hostname
		if name == "" {
			name = "Unnamed host"
		}
		fmt.Fprintf(
			&output, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			history.Period, sanitizeTXT(name), sanitizeTXT(record.MAC),
			sanitizeTXT(strings.Join(ipv4, ", ")),
			sanitizeTXT(strings.Join(ipv6, ", ")),
			record.Downloaded, record.Uploaded,
			formatBytes(record.Downloaded), formatBytes(record.Uploaded),
		)
	}
	fmt.Fprintf(
		&output, "%s\tTOTAL\t\t\t\t%d\t%d\t%s\t%s\n",
		history.Period, history.Totals.Downloaded, history.Totals.Uploaded,
		formatBytes(history.Totals.Downloaded), formatBytes(history.Totals.Uploaded),
	)
	filename := fmt.Sprintf(
		"op-flow-%s-%s.txt", granularity,
		strings.NewReplacer("/", "-", " ", "-").Replace(history.Period),
	)
	return filename, output.String(), nil
}

func sanitizeTXT(value string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
}

func formatBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := uint64(unit), 0
	for quotient := value / unit; quotient >= unit && exponent < 5; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf(
		"%.2f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent],
	)
}

func (t *Tracker) usagePeriodsLocked() map[string][]string {
	return map[string][]string{
		"day":     t.periodOptionsLocked("day"),
		"month":   t.periodOptionsLocked("month"),
		"quarter": t.periodOptionsLocked("quarter"),
		"year":    t.periodOptionsLocked("year"),
	}
}

func (t *Tracker) periodOptionsLocked(granularity string) []string {
	unique := make(map[string]bool)
	now := time.Now().In(time.Local)
	unique[formatPeriod(granularity, now)] = true
	for day := range t.daily {
		value, err := time.ParseInLocation("2006-01-02", day, time.Local)
		if err == nil {
			unique[formatPeriod(granularity, value)] = true
		}
	}
	out := make([]string, 0, len(unique))
	for value := range unique {
		out = append(out, value)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func formatPeriod(granularity string, value time.Time) string {
	switch granularity {
	case "day":
		return value.Format("2006-01-02")
	case "month":
		return value.Format("2006-01")
	case "quarter":
		return fmt.Sprintf("%04d-Q%d", value.Year(), (int(value.Month())-1)/3+1)
	default:
		return value.Format("2006")
	}
}

func validPeriod(granularity, period string) bool {
	switch granularity {
	case "day":
		_, err := time.ParseInLocation("2006-01-02", period, time.Local)
		return err == nil
	case "month":
		_, err := time.ParseInLocation("2006-01", period, time.Local)
		return err == nil
	case "quarter":
		if len(period) != 7 || period[4:6] != "-Q" {
			return false
		}
		year, yearErr := strconv.Atoi(period[:4])
		quarter, quarterErr := strconv.Atoi(period[6:])
		return yearErr == nil && quarterErr == nil && year >= 1 &&
			quarter >= 1 && quarter <= 4
	case "year":
		year, err := strconv.Atoi(period)
		return err == nil && len(period) == 4 && year >= 1
	default:
		return false
	}
}

func periodContains(granularity, period, day string) bool {
	value, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return false
	}
	return formatPeriod(granularity, value) == period
}

func primaryProfileIP(record model.UsageRecord) string {
	addresses := uniqueHostAddresses(record.Addresses)
	if len(addresses) > 0 {
		return addresses[0].IP
	}
	return record.HostID
}

func cloneDaily(
	input map[string]map[string]model.TrafficUsage,
) map[string]map[string]model.TrafficUsage {
	output := make(map[string]map[string]model.TrafficUsage, len(input))
	for day, records := range input {
		output[day] = make(map[string]model.TrafficUsage, len(records))
		for id, usage := range records {
			output[day][id] = usage
		}
	}
	return output
}

func cloneProfiles(
	input map[string]model.HostProfile,
) map[string]model.HostProfile {
	output := make(map[string]model.HostProfile, len(input))
	for id, profile := range input {
		profile.Addresses = append([]model.HostAddress(nil), profile.Addresses...)
		output[id] = profile
	}
	return output
}

func cloneStrings(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
