package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func validateExactWorkFlags(args []string, allowed map[string]struct{}) error {
	seen := make(map[string]struct{}, len(allowed))
	for _, argument := range args {
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			continue
		}
		if !strings.HasPrefix(argument, "--") {
			return fmt.Errorf("flag provided but not defined: %s", argument)
		}
		name := strings.TrimPrefix(argument, "--")
		if separator := strings.IndexByte(name, '='); separator >= 0 {
			name = name[:separator]
		}
		if _, ok := allowed[name]; !ok || name == "" {
			return fmt.Errorf("flag provided but not defined: -%s", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("work command flag --%s was provided more than once", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func encodeWorkJSON(stdout io.Writer, value any) error {
	if value == nil {
		return fmt.Errorf("work provider returned no result")
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
