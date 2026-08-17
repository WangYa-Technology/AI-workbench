package discovery

import (
	"strconv"
	"testing"
)

func TestRolloutBucketIsStableAndCoversBaselineAndCandidateCohorts(t *testing.T) {
	if rolloutBucket("architectural|product") != rolloutBucket("architectural|product") {
		t.Fatal("the same public query moved between rollout cohorts")
	}
	candidate, baseline := false, false
	for index := 0; index < 1000; index++ {
		if rolloutBucket(strconv.Itoa(index)+"|product") < 25 {
			candidate = true
		} else {
			baseline = true
		}
	}
	if !candidate || !baseline {
		t.Fatalf("25 percent rollout did not expose both cohorts: candidate=%t baseline=%t", candidate, baseline)
	}
}
