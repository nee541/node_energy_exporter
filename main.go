package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/procfs/sysfs"
)

const (
	raplPath = "/sys/class/powercap/intel-rapl/"
	sysPath  = "/sys/"
)

func init() {
	log.SetOutput(os.Stdout)
}

var (
	ErrNoData       = errors.New("collector returned no data")
	metricNameRegex = regexp.MustCompile(`_*[^0-9A-Za-z_]+_*`)
)

type RaplCollector struct {
	fs               sysfs.FS
	joulesMetricDesc *prometheus.Desc
}

// SanitizeMetricName sanitize the given metric name by replacing invalid characters by underscores.
//
// OpenMetrics and the Prometheus exposition format require the metric name
// to consist only of alphanumericals and "_", ":" and they must not start
// with digits. Since colons in MetricFamily are reserved to signal that the
// MetricFamily is the result of a calculation or aggregation of a general
// purpose monitoring system, colons will be replaced as well.
//
// Note: If not subsequently prepending a namespace and/or subsystem (e.g.,
// with prometheus.BuildFQName), the caller must ensure that the supplied
// metricName does not begin with a digit.
func SanitizeMetricName(metricName string) string {
	return metricNameRegex.ReplaceAllString(metricName, "_")
}

func NewRaplCollector() (*RaplCollector, error) {
	fs, err := sysfs.NewFS(sysPath)
	if err != nil {
		return nil, err
	}
	joulesMetricDesc := prometheus.NewDesc(
		"node_rapl_joules_total",
		"Current RAPL value in joules",
		[]string{"instance", "package", "domain"},
		nil,
	)
	collector := RaplCollector{
		fs:               fs,
		joulesMetricDesc: joulesMetricDesc,
	}
	return &collector, nil
}

// Update implements Collector and exposes RAPL related metrics.
func (c *RaplCollector) Update(ch chan<- prometheus.Metric) error {
	// nil zones are fine when platform doesn't have powercap files present.
	zones, err := sysfs.GetRaplZones(c.fs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Println("msg", "Platform doesn't have powercap files present", "err", err)
			return ErrNoData
		}
		if errors.Is(err, os.ErrPermission) {
			log.Println("msg", "Can't access powercap files", "err", err)
			return ErrNoData
		}
		return fmt.Errorf("failed to retrieve rapl stats: %w", err)
	}

	for _, rz := range zones {
		microJoules, err := rz.GetEnergyMicrojoules()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				log.Println("msg", "Can't access energy_uj file", "zone", rz, "err", err)
				return ErrNoData
			}
			return err
		}

		joules := float64(microJoules) / 1000000.0

		ch <- c.joulesMetric(rz, joules)
	}
	return nil
}

func (c *RaplCollector) joulesMetric(z sysfs.RaplZone, v float64) prometheus.Metric {
	index := strconv.Itoa(z.Index)
	descriptor := prometheus.NewDesc(
		prometheus.BuildFQName(
			"node",
			"rapl",
			fmt.Sprintf("%s_joules_total", SanitizeMetricName(z.Name)),
		),
		fmt.Sprintf("Current RAPL %s value in joules", z.Name),
		[]string{"index", "path"}, nil,
	)

	return prometheus.MustNewConstMetric(
		descriptor,
		prometheus.CounterValue,
		v,
		index,
		z.Path,
	)
}

func (c *RaplCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.joulesMetricDesc
}

func (c *RaplCollector) Collect(ch chan<- prometheus.Metric) {
	if err := c.Update(ch); err != nil {
		log.Println("msg", "failed to update rapl stats", "err", err)
	}
}

// For testing purposes
// func iterateOverPowercap() {
// 	fs, err := sysfs.NewFS(sysPath)
// 	if err != nil {
// 		log.Println("msg", "failed to create sysfs", "err", err)
// 		return
// 	}
// 	zones, err := sysfs.GetRaplZones(fs)
// 	if err != nil {
// 		log.Println("msg", "failed to retrieve rapl stats", "err", err)
// 		return
// 	}
// 	for _, rz := range zones {
// 		fmt.Println("name", rz.Name, "sanitized name", SanitizeMetricName(rz.Name), "path", rz.Path, "index", rz.Index)
// 	}
// }

func main() {
	reg := prometheus.NewRegistry()
	raplCollector, err := NewRaplCollector()
	if err != nil {
		log.Println("NewFS error", err)
	}
	reg.MustRegister(raplCollector)

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	http.ListenAndServe(":9110", nil)
}
