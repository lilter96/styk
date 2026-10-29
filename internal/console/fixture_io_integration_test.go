//go:build integration

package console

import (
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

func fixturePrintf(t testing.TB, writer io.Writer, format string, values ...any) {
	t.Helper()
	if _, err := fmt.Fprintf(writer, format, values...); err != nil {
		t.Errorf("write synthetic fixture response: %v", err)
	}
}

func fixtureText(t testing.TB, writer io.Writer, values ...any) {
	t.Helper()
	if _, err := fmt.Fprint(writer, values...); err != nil {
		t.Errorf("write synthetic fixture response: %v", err)
	}
}

func closeFixture(t testing.TB, stream io.Closer) {
	t.Helper()
	if err := stream.Close(); err != nil {
		t.Errorf("close fixture response: %v", err)
	}
}

func removeFixtureFile(t testing.TB, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remove fixture file: %v", err)
	}
}
