package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	raplPath = "/sys/class/powercap/intel-rapl/"
)

type raplCollector struct {
	mu                  sync.Mutex
	instance            string
	lastEnergyUj        map[string]float64
	energyConsumedDelta *prometheus.Desc
}

func newRaplCollector(instance string) *raplCollector {
	return &raplCollector{
		instance: instance,
		lastEnergyUj: make(map[string]float64),
		energyConsumedDelta: prometheus.NewDesc("rapl_energy_consumed_delta_uj",
			"Delta energy consumption in microjoules since last scrape",
			[]string{"instance", "package", "domain"},
			nil,
		),
	}
}

func (collector *raplCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.energyConsumedDelta
}

func readFileContents(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	contents, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(contents)), nil
}

func (collector *raplCollector) Collect(ch chan<- prometheus.Metric) {
	collector.mu.Lock()
	defer collector.mu.Unlock()

	pkgs, _ := os.ReadDir(raplPath)
	for _, pkg := range pkgs {
		pkgName := pkg.Name()
		if !strings.Contains(pkgName, "intel-rapl:") {
			continue
		}

		pkgEnergy, err := readFileContents(fmt.Sprintf("%s%s/energy_uj", raplPath, pkgName))
		if err != nil {
			log.Println("Error reading file:", err)
			continue
		}

		value, _ := strconv.ParseFloat(pkgEnergy, 64)
		key := fmt.Sprintf("%s-package", pkgName)
		delta := value - collector.lastEnergyUj[key]
		if delta < 0 {
			maxEnergy, _ := readFileContents(fmt.Sprintf("%s%s/max_energy_range_uj", raplPath, pkgName))
			maxValue, _ := strconv.ParseFloat(maxEnergy, 64)
			delta += maxValue
		}
		collector.lastEnergyUj[key] = value
		ch <- prometheus.MustNewConstMetric(collector.energyConsumedDelta, prometheus.GaugeValue, delta, collector.instance, pkgName, "package")

		// Subdomains
		domains, _ := os.ReadDir(raplPath + pkgName)
		for _, domain := range domains {
			if !strings.Contains(domain.Name(), "intel-rapl:") {
				continue
			}

			domainEnergy, err := readFileContents(fmt.Sprintf("%s%s/%s/energy_uj", raplPath, pkgName, domain.Name()))
			if err != nil {
				log.Println("Error reading domain file:", err)
				continue
			}

			value, _ = strconv.ParseFloat(domainEnergy, 64)
			key = fmt.Sprintf("%s-%s", pkgName, domain.Name())
			delta = value - collector.lastEnergyUj[key]
			if delta < 0 {
				maxEnergy, _ := readFileContents(fmt.Sprintf("%s%s/%s/max_energy_range_uj", raplPath, pkgName, domain.Name()))
				maxValue, _ := strconv.ParseFloat(maxEnergy, 64)
				delta += maxValue
			}
			collector.lastEnergyUj[key] = value
			ch <- prometheus.MustNewConstMetric(collector.energyConsumedDelta, prometheus.GaugeValue, delta, collector.instance, pkgName, domain.Name())
		}
	}
}

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("Could not get hostname: %v", err)
	}

	r := prometheus.NewRegistry()
	r.MustRegister(newRaplCollector(hostname))

	http.Handle("/metrics", promhttp.HandlerFor(r, promhttp.HandlerOpts{}))
	http.ListenAndServe(":9100", nil)
}