package imperva

import "testing"

func TestLatestCompletePointSortsByTimestamp(t *testing.T) {
	point, err := latestCompletePoint(TSData{
		Id: "api.stats.bandwidth_timeseries.bandwidth",
		Data: [][]int64{
			{3000, 30},
			{1000, 10},
			{2000, 20},
		},
	})
	if err != nil {
		t.Fatalf("latestCompletePoint returned error: %v", err)
	}
	if point[0] != 2000 || point[1] != 20 {
		t.Fatalf("expected second latest point [2000 20], got %v", point)
	}
}

func TestSummaryValueRejectsInvalidValueType(t *testing.T) {
	_, _, err := summaryValue([]interface{}{"PE", "bad"}, "api.stats.visits_dist_summary.country")
	if err == nil {
		t.Fatal("expected invalid value type error")
	}
}

func TestNormalizeAPIBaseURLRequiresHTTPS(t *testing.T) {
	if _, err := normalizeAPIBaseURL("http://my.incapsula.com/api/"); err == nil {
		t.Fatal("expected http base URL to be rejected")
	}

	got, err := normalizeAPIBaseURL("https://my.incapsula.com/api")
	if err != nil {
		t.Fatalf("expected https base URL to be accepted: %v", err)
	}
	if got != "https://my.incapsula.com/api/" {
		t.Fatalf("expected normalized trailing slash, got %q", got)
	}
}
