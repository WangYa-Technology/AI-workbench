package assets_test

import (
	"github.com/hcai-chat/hcai-chat/internal/testutil"
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunWithDatabases(m)) }
