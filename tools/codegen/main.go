// Command codegen generates the sonarqube client from the endpoint and
// parameter matrices in docs/ and the API metadata harvested from SonarQube
// images into spec/harvest.
//
//	cd tools/codegen && go run . -root ../..
//
// Output goes to the sonarqube/ package: services_gen.go, models_gen.go,
// registry_gen.go, one api_<service>_gen.go per service and the unit-test
// table api_cases_gen_test.go.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", "../..", "repository root")
	verbose := flag.Bool("v", false, "print warnings")
	flag.Parse()
	if err := run(*root, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "codegen:", err)
		os.Exit(1)
	}
}

func run(root string, verbose bool) error {
	sh, err := readSheets(
		filepath.Join(root, "docs", "sonarqube-client-go endpoint × SonarQube version matrix.xlsx"),
		filepath.Join(root, "docs", "sonarqube-client-go parameter changes per SonarQube version.xlsx"),
	)
	if err != nil {
		return err
	}
	sheetColumns = sh.Columns
	var hs []*harvest
	// Community Edition first: its metadata wins for shared endpoints.
	for _, d := range []string{"ce-26.11", "ee-2026.5.5"} {
		h, err := loadHarvest(filepath.Join(root, "spec", "harvest", d))
		if err != nil {
			return err
		}
		hs = append(hs, h)
	}
	overrides := map[string]string{}
	if err := readJSON(filepath.Join(root, "spec", "type-overrides.json"), &overrides); err != nil && !os.IsNotExist(err) {
		return err
	}
	b := &builder{sheets: sh, harvests: hs, types: newTypes("Paging", "Client", "Response", "Request", "File", "ErrorResponse", "EndpointInfo", "ParamInfo", "Option"), typeOverrides: overrides}
	if err := b.build(); err != nil {
		return err
	}
	if err := b.emit(filepath.Join(root, "sonarqube")); err != nil {
		return err
	}
	if verbose {
		for _, w := range b.warnings {
			fmt.Fprintln(os.Stderr, "warning:", w)
		}
	}
	fmt.Printf("generated %d endpoints, %d model types\n", len(b.endpoints), len(b.types.structs))
	return nil
}
