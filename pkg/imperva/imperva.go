package imperva

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kofalt/go-memoize"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/xciber/imperva-exporter/pkg/metrics"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	baseApiUrl       = "https://my.incapsula.com/api/"
	siteListEndpoint = "prov/v1/sites/list"
	statsApiEndpoint = "stats/v1"
	maxErrorBodySize = 4096
)

type Client struct {
	httpClient   *http.Client
	logger       *slog.Logger
	baseURL      string
	clientId     string
	clientSecret string
	Sites        map[string]SiteDesc
	cache        *memoize.Memoizer
	metrics      map[string]*metrics.MetricInfo
	//workers      int
}

type SiteDesc struct {
	SiteId                               int      `json:"site_id"`
	Status                               string   `json:"status"`
	Domain                               string   `json:"domain"`
	AccountId                            int      `json:"account_id"`
	AccelerationLevel                    string   `json:"acceleration_level"`
	AccelerationLevelRaw                 string   `json:"acceleration_level_raw"`
	SiteCreationDate                     int64    `json:"site_creation_date"`
	Ips                                  []string `json:"ips"`
	Active                               string   `json:"active"`
	SupportAllTlsVersions                bool     `json:"support_all_tls_versions"`
	UseWildcardSanInsteadOfFullDomainSan bool     `json:"use_wildcard_san_instead_of_full_domain_san"`
	AddNakedDomainSan                    bool     `json:"add_naked_domain_san"`
	DisplayName                          string   `json:"display_name"`
	Security                             struct {
		Waf struct {
			Rules []struct {
				Action                 string `json:"action,omitempty"`
				ActionText             string `json:"action_text,omitempty"`
				Id                     string `json:"id"`
				Name                   string `json:"name"`
				BlockBadBots           bool   `json:"block_bad_bots,omitempty"`
				ChallengeSuspectedBots bool   `json:"challenge_suspected_bots,omitempty"`
				ActivationMode         string `json:"activation_mode,omitempty"`
				ActivationModeText     string `json:"activation_mode_text,omitempty"`
				DdosTrafficThreshold   int    `json:"ddos_traffic_threshold,omitempty"`
			} `json:"rules"`
		} `json:"waf"`
	} `json:"security"`
	Res        int    `json:"res"`
	ResMessage string `json:"res_message"`
}

type SiteListResponse struct {
	Sites      []SiteDesc `json:"Sites"`
	Res        int        `json:"res"`
	ResMessage string     `json:"res_message"`
	DebugInfo  struct {
		IdInfo string `json:"id-info"`
	} `json:"debug_info"`
}

type TSData struct {
	Data [][]int64 `json:"data"`
	Id   string    `json:"id"`
	Name string    `json:"name"`
}

type StatsTimeSeriesResponse struct {
	BandwidthTimeSeries []TSData `json:"bandwidth_timeseries,omitempty"`
	VisitsTimeSeries    []TSData `json:"visits_timeseries,omitempty"`
	HitsTimeSeries      []TSData `json:"hits_timeseries,omitempty"`
	Res                 int      `json:"res"`
	ResMessage          string   `json:"res_message"`
	DebugInfo           struct {
		IdInfo string `json:"id-info"`
	} `json:"debug_info"`
}

type SumData struct {
	Data [][]interface{} `json:"data"`
	Id   string          `json:"id"`
	Name string          `json:"name"`
}

type StatsSummaryResponse struct {
	RequestsGeoDistSummary SumData   `json:"requests_geo_dist_summary"`
	VisitsDistSummary      []SumData `json:"visits_dist_summary"`
	Res                    int       `json:"res"`
	ResMessage             string    `json:"res_message"`
}

func (c *Client) post(path string) ([]byte, error) {
	return c.postWithParams(path, nil)
}

func (c *Client) postWithParams(path string, param map[string]string) ([]byte, error) {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		c.logger.Error("Error building request URL", "error", err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, nil)
	if err != nil {
		c.logger.Error("Error creating request", "error", err)
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("x-API-Id", c.clientId)
	req.Header.Add("x-API-Key", c.clientSecret)

	p := req.URL.Query()
	for k, v := range param {
		p[k] = append(p[k], v)
	}

	req.URL.RawQuery = p.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Error sending request", "error", err)
		return nil, err
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			c.logger.Error("Error closing response body", "error", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		err := fmt.Errorf("imperva API request to %s returned %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
		c.logger.Error("Error response from server", "status", resp.Status, "path", path)
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.logger.Error("Error reading response body", "error", err)
		return nil, err
	}

	return body, nil
}

func (c *Client) DescribeMetrics(ch chan<- *prometheus.Desc) {
	for _, m := range c.metrics {
		ch <- m.Desc
	}
}

func (c *Client) UpdateSiteList() error {

	c.logger.Debug("updating site list")

	const pageSize = 100
	page := 0

	res := make([]SiteDesc, 0)

	for {
		siteList, err, cached := c.cache.Memoize("siteList"+strconv.Itoa(page),
			func() (interface{}, error) {
				return c.postWithParams(siteListEndpoint, map[string]string{
					"page_size": strconv.Itoa(pageSize),
					"page_num":  strconv.Itoa(page),
				})
			})
		if err != nil {
			return err
		}

		c.logger.Debug("site list call", "page", page, "cached", cached)

		sr := &SiteListResponse{}
		err = json.Unmarshal(siteList.([]byte), sr)
		if err != nil {
			c.logger.Error("Error unmarshalling response", "error", err)
			return err
		}
		if sr.Res != 0 {
			return fmt.Errorf("imperva site list failed: res=%d message=%q", sr.Res, sr.ResMessage)
		}
		if len(sr.Sites) == 0 {
			break
		}
		res = append(res, sr.Sites...)
		page++
	}
	for _, r := range res {
		c.Sites[r.Domain] = r
	}
	return nil
}

func (c *Client) getSiteWafMetrics(domain string) ([]*prometheus.Metric, error) {

	c.logger.Debug("getting site waf metrics", "domain", domain)

	res := make([]*prometheus.Metric, 0)

	if s, found := c.Sites[domain]; found {
		for _, rule := range s.Security.Waf.Rules {
			if rule.Id == "api.threats.ddos" {
				res = append(res, c.metrics["ddos_threshold"].GetPromMetric(float64(rule.DdosTrafficThreshold), []string{domain, rule.ActivationModeText}))
			}
		}
	} else {
		c.logger.Error("Site not found", "domain", domain)
		return nil, fmt.Errorf("site not found")
	}
	return res, nil
}

func summaryValue(datum []interface{}, metricID string) (string, float64, error) {
	if len(datum) != 2 {
		return "", 0, fmt.Errorf("invalid data format for metric %s", metricID)
	}

	key, ok := datum[0].(string)
	if !ok {
		return "", 0, fmt.Errorf("invalid key type for metric %s", metricID)
	}
	if key == "" {
		key = "unknown"
	}

	switch val := datum[1].(type) {
	case float64:
		return key, val, nil
	case int:
		return key, float64(val), nil
	case int64:
		return key, float64(val), nil
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return "", 0, fmt.Errorf("invalid numeric value for metric %s: %w", metricID, err)
		}
		return key, f, nil
	default:
		return "", 0, fmt.Errorf("invalid value type for metric %s", metricID)
	}
}

func (c *Client) sumToMetric(domain string, data SumData) ([]*prometheus.Metric, error) {

	res := make([]*prometheus.Metric, 0)
	metricName := ""

	switch data.Id {
	case "api.stats.requests_geo_dist_summary.datacenter":
		metricName = "geo_dc"
	case "api.stats.visits_dist_summary.country":
		metricName = "visits_country"
	case "api.stats.visits_dist_summary.client_app":
		metricName = "visits_client"
	default:
		return nil, fmt.Errorf("unknown metric %s", data.Id)
	}

	keys := make(map[string]struct{})
	for _, datum := range data.Data {
		key, val, err := summaryValue(datum, data.Id)
		if err != nil {
			return nil, err
		}
		if _, found := keys[key]; !found {
			keys[key] = struct{}{}
			res = append(res, c.metrics[metricName].GetPromMetric(val, []string{domain, key}))
		}
	}

	return res, nil
}

func latestCompletePoint(tsData TSData) ([]int64, error) {
	if len(tsData.Data) < 2 {
		return nil, fmt.Errorf("no data for metric %s", tsData.Id)
	}

	data := make([][]int64, 0, len(tsData.Data))
	for _, point := range tsData.Data {
		if len(point) != 2 {
			return nil, fmt.Errorf("wrong number of data for metric %s", tsData.Id)
		}
		data = append(data, point)
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	return data[len(data)-2], nil
}

func (c *Client) tsdToMetric(domain string, tsData TSData) (*prometheus.Metric, error) {
	point, err := latestCompletePoint(tsData)
	if err != nil {
		return nil, err
	}

	switch tsData.Id {
	case "api.stats.bandwidth_timeseries.bandwidth":
		return c.metrics["bandwidth"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.bandwidth_timeseries.bps":
		return c.metrics["bps"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.hits_timeseries.human":
		return c.metrics["hits_human"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.hits_timeseries.human_ps":
		return c.metrics["hits_human_rps"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.hits_timeseries.bot":
		return c.metrics["hits_bot"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.hits_timeseries.bot_ps":
		return c.metrics["hits_bot_rps"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.hits_timeseries.blocked":
		return c.metrics["hits_blocked"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.hits_timeseries.blocked_ps":
		return c.metrics["hits_blocked_rps"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.visits_timeseries.human":
		return c.metrics["visits_human"].GetPromMetric(float64(point[1]), []string{domain}), nil
	case "api.stats.visits_timeseries.bot":
		return c.metrics["visits_bot"].GetPromMetric(float64(point[1]), []string{domain}), nil
	default:
		return nil, fmt.Errorf("unknown metric %s", tsData.Id)
	}
}

func (c *Client) getSiteSumMetrics(domain string) ([]*prometheus.Metric, error) {
	c.logger.Debug("getting site summary metrics", "domain", domain)

	s, found := c.Sites[domain]
	if !found {
		return nil, fmt.Errorf("site not found")
	}

	res := make([]*prometheus.Metric, 0)

	data, err, cached := c.cache.Memoize("Summary_"+s.Domain,
		func() (interface{}, error) {
			return c.postWithParams(
				statsApiEndpoint,
				map[string]string{
					"site_id":    strconv.Itoa(s.SiteId),
					"stats":      "requests_geo_dist_summary,visits_dist_summary",
					"time_range": "today",
				})
		})
	if err != nil {
		c.logger.Error("Error getting summary", "domain", domain, "error", err)
		return nil, err
	}
	c.logger.Debug("got site summary metrics", "domain", domain, "cached", cached)

	sr := &StatsSummaryResponse{}
	err = json.Unmarshal(data.([]byte), sr)
	if err != nil {
		c.logger.Error("Error unmarshalling summary response", "domain", domain, "error", err)
		return nil, err
	}
	if sr.Res != 0 {
		return nil, fmt.Errorf("imperva summary failed for %s: res=%d message=%q", domain, sr.Res, sr.ResMessage)
	}

	c.logger.Debug("summary metrics unmarshalled", "domain", domain)

	m, err := c.sumToMetric(domain, sr.RequestsGeoDistSummary)
	if err != nil {
		c.logger.Error("Error converting sum data to metric", "error", err)
	}
	res = append(res, m...)

	for _, sumData := range sr.VisitsDistSummary {
		m, err := c.sumToMetric(domain, sumData)
		if err != nil {
			c.logger.Error("Error converting sum data to metric", "error", err)
			continue
		}
		res = append(res, m...)
	}

	return res, nil
}

func (c *Client) getSiteTSMetrics(domain string) ([]*prometheus.Metric, error) {

	c.logger.Debug("getting site ts metrics", "domain", domain)

	s, found := c.Sites[domain]
	if !found {
		return nil, fmt.Errorf("site not found: %s", domain)
	}

	res := make([]*prometheus.Metric, 0)

	data, err, cached := c.cache.Memoize("TimeSeries_"+s.Domain,
		func() (interface{}, error) {
			return c.postWithParams(
				statsApiEndpoint,
				map[string]string{
					"site_id":     strconv.Itoa(s.SiteId),
					"stats":       "bandwidth_timeseries,hits_timeseries,visits_timeseries",
					"time_range":  "today",
					"granularity": "300000",
				})
		})
	if err != nil {
		c.logger.Error("Error getting time series", "domain", domain, "error", err)
		return nil, err
	}
	c.logger.Debug("got site ts metrics", "domain", domain, "cached", cached)

	tsr := &StatsTimeSeriesResponse{}
	err = json.Unmarshal(data.([]byte), tsr)
	if err != nil {
		c.logger.Error("Error unmarshalling response", "domain", domain, "error", err)
		return nil, err
	}
	if tsr.Res != 0 {
		return nil, fmt.Errorf("imperva time series failed for %s: res=%d message=%q", domain, tsr.Res, tsr.ResMessage)
	}

	c.logger.Debug("metrics unmarshalled", "domain", domain)

	for _, tsData := range tsr.BandwidthTimeSeries {
		m, err := c.tsdToMetric(domain, tsData)
		if err != nil {
			c.logger.Error("Error converting time series data to metric", "error", err)
			continue
		}
		res = append(res, m)
	}
	c.logger.Debug("bandwidth metrics sent", "domain", domain)

	for _, tsData := range tsr.HitsTimeSeries {
		m, err := c.tsdToMetric(domain, tsData)
		if err != nil {
			c.logger.Error("Error converting time series data to metric", "error", err)
			continue
		}
		res = append(res, m)
	}
	c.logger.Debug("hits metrics sent", "domain", domain)

	for _, tsData := range tsr.VisitsTimeSeries {
		m, err := c.tsdToMetric(domain, tsData)
		if err != nil {
			c.logger.Error("Error converting time series data to metric", "error", err)
			continue
		}
		res = append(res, m)
	}
	c.logger.Debug("visits metrics sent", "domain", domain)

	return res, nil
}

func (c *Client) GetMetricsByDomain(domain string) ([]*prometheus.Metric, error) {
	res := make([]*prometheus.Metric, 0)

	m, err := c.getSiteWafMetrics(domain)
	if err != nil {
		return nil, err
	}
	res = append(res, m...)

	m, err = c.getSiteTSMetrics(domain)
	if err != nil {
		return nil, err
	}
	res = append(res, m...)

	m, err = c.getSiteSumMetrics(domain)
	if err != nil {
		return nil, err
	}
	res = append(res, m...)

	return res, nil
}

func normalizeAPIBaseURL(apiBaseURL string) (string, error) {
	if apiBaseURL == "" {
		apiBaseURL = baseApiUrl
	}
	apiBaseURL = strings.TrimRight(apiBaseURL, "/") + "/"

	parsed, err := url.Parse(apiBaseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("api base url must be an absolute https URL")
	}

	return apiBaseURL, nil
}

func NewClient(id string, secret string, logger *slog.Logger, timeout int, ttl int, apiBaseURL string) *Client {
	apiBaseURL, err := normalizeAPIBaseURL(apiBaseURL)
	if err != nil {
		logger.Error("Invalid Imperva API base URL, falling back to default", "error", err)
		apiBaseURL = baseApiUrl
	}

	c := &Client{
		httpClient: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
		logger:       logger,
		baseURL:      apiBaseURL,
		clientId:     id,
		clientSecret: secret,
		metrics:      make(map[string]*metrics.MetricInfo),
		cache:        memoize.NewMemoizer(time.Duration(ttl)*time.Second, time.Minute),
		Sites:        make(map[string]SiteDesc),
	}

	// declare metrics
	c.metrics["up"] = metrics.NewMetric("up", "Was the last scrape of Imperva successful.", prometheus.GaugeValue, "imperva", "", nil, nil)
	c.metrics["ddos_threshold"] = metrics.NewMetric("ddos_threshold", "DDoS Threshold", prometheus.GaugeValue, "imperva", "waf", []string{"domain", "mode"}, nil)
	c.metrics["bandwidth"] = metrics.NewMetric("bandwidth", "Site bandwidth", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["bps"] = metrics.NewMetric("bps", "Site bandwidth in bps", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["hits_human"] = metrics.NewMetric("hits_human", "Site hits from humans", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["hits_human_rps"] = metrics.NewMetric("hits_human_rps", "Site hits from humans per second", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["hits_bot"] = metrics.NewMetric("hits_bot", "Site hits from bots", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["hits_bot_rps"] = metrics.NewMetric("hits_bot_rps", "Site hits from bots per second", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["hits_blocked"] = metrics.NewMetric("hits_blocked", "Site hits blocked", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["hits_blocked_rps"] = metrics.NewMetric("hits_blocked_rps", "Site hits blocked per second", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["visits_human"] = metrics.NewMetric("visits_human", "Site visits from humans", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["visits_bot"] = metrics.NewMetric("visits_bot", "Site visits from bots", prometheus.GaugeValue, "imperva", "stats", []string{"domain"}, nil)
	c.metrics["geo_dc"] = metrics.NewMetric("geo_dc", "Requests by data-center location", prometheus.GaugeValue, "imperva", "stats", []string{"domain", "idc"}, nil)
	c.metrics["visits_country"] = metrics.NewMetric("visits_country", "Visits by country", prometheus.GaugeValue, "imperva", "stats", []string{"domain", "country"}, nil)
	c.metrics["visits_client"] = metrics.NewMetric("visits_client", "Visits by client application", prometheus.GaugeValue, "imperva", "stats", []string{"domain", "client"}, nil)

	// initial update of site list
	err = c.UpdateSiteList()
	if err != nil {
		c.logger.Error("Error updating site list", "error", err)
	}

	return c
}
