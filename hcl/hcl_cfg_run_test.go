// Copyright 2022 Mineiros GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hcl_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/madlambda/spells/assert"
	"github.com/mineiros-io/terramate/errors"
	"github.com/mineiros-io/terramate/hcl"
	"github.com/mineiros-io/terramate/hcl/ast"
)

func TestHCLParserConfigRun(t *testing.T) {
	runEnvCfg := func(hcldoc string) hcl.Config {
		// Comparing attributes/expressions with hcl/hclsyntax is impossible
		// We generate the code from the expressions in order to compare it but for that
		// we need an origin file/data to get the tokens for each expression,
		// hence all this x_x.
		tmpdir := t.TempDir()
		filepath := filepath.Join(tmpdir, "test_file.hcl")
		assert.NoError(t, os.WriteFile(filepath, []byte(hcldoc), 0700))

		parser := hclparse.NewParser()
		res, diags := parser.ParseHCLFile(filepath)
		if diags.HasErrors() {
			t.Fatalf("test case provided invalid hcl, error: %v hcl:\n%s", diags, hcldoc)
		}

		body := res.Body.(*hclsyntax.Body)
		attrs := make(ast.Attributes)

		for name, attr := range body.Attributes {
			attrs[name] = ast.NewAttribute(filepath, attr)
		}

		return hcl.Config{
			Terramate: &hcl.Terramate{
				Config: &hcl.RootConfig{
					Run: &hcl.RunConfig{
						Env: &hcl.RunEnv{
							Attributes: attrs,
						},
					},
				},
			},
		}
	}

	for _, tc := range []testcase{
		{
			name: "empty run",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `terramate {
					  config {
					    run {
					    }
					  }
					}`,
				},
			},
			want: want{
				config: hcl.Config{
					Terramate: &hcl.Terramate{
						Config: &hcl.RootConfig{
							Run: &hcl.RunConfig{},
						},
					},
				},
			},
		},
		{
			name: "empty run.env",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `terramate {
					  config {
					    run {
					      env {
					      }
					    }
					  }
					}`,
				},
			},
			want: want{
				config: hcl.Config{
					Terramate: &hcl.Terramate{
						Config: &hcl.RootConfig{
							Run: &hcl.RunConfig{
								Env: &hcl.RunEnv{},
							},
						},
					},
				},
			},
		},
		{
			name: "unrecognized attribute on run",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      something = "bleh"
						    }
						  }
						}
					`,
				},
			},
			want: want{
				errs: []error{
					errors.E(hcl.ErrTerramateSchema),
				},
			},
		},
		{
			name: "unrecognized block on run",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      something {
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				errs: []error{
					errors.E(hcl.ErrTerramateSchema),
				},
			},
		},
		{
			name: "unrecognized label on run",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run "invalid" {
						    }
						  }
						}
					`,
				},
			},
			want: want{
				errs: []error{
					errors.E(hcl.ErrTerramateSchema),
				},
			},
		},
		{
			name: "unrecognized label on run.env",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      env "invalid" {
						        something = "bleh"
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				errs: []error{
					errors.E(hcl.ErrTerramateSchema),
				},
			},
		},
		{
			name: "multiple empty run blocks on same config",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {}
						    run {}
						  }
						}
					`,
				},
			},
			want: want{
				config: hcl.Config{
					Terramate: &hcl.Terramate{
						Config: &hcl.RootConfig{
							Run: &hcl.RunConfig{},
						},
					},
				},
			},
		},
		{
			name: "multiple empty run blocks on multiple config",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {}
						  }
						  config {
						    run {}
						  }
						}
					`,
				},
			},
			want: want{
				config: hcl.Config{
					Terramate: &hcl.Terramate{
						Config: &hcl.RootConfig{
							Run: &hcl.RunConfig{},
						},
					},
				},
			},
		},
		{
			name: "attrs on run.env in single block/file",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						        number = 666
						        list = []
						        interp = "${global.a}"
						        traversal = global.a.b
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				config: runEnvCfg(`
						string = "value"
						number = 666
						list = []
						interp = "${global.a}"
						traversal = global.a.b
				`),
			},
		},
		{
			name: "multiple run blocks on same file are merged",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						        number = 666
						      }
						    }
						    run {
						      env {
						        list = []
						        interp = "${global.a}"
						        traversal = global.a.b
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				config: runEnvCfg(`
						string = "value"
						number = 666
						list = []
						interp = "${global.a}"
						traversal = global.a.b
				`),
			},
		},
		{
			name: "multiple env blocks on same file are merged",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						        number = 666
						      }
						      env {
						        list = []
						        interp = "${global.a}"
						        traversal = global.a.b
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				config: runEnvCfg(`
						string = "value"
						number = 666
						list = []
						interp = "${global.a}"
						traversal = global.a.b
				`),
			},
		},
		{
			name: "env defined on multiple files are merged",
			input: []cfgfile{
				{
					filename: "cfg1.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						      }
						    }
						  }
						}
					`,
				},
				{
					filename: "cfg2.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        number = 666
						        list = []
						      }
						    }
						  }
						}
					`,
				},
				{
					filename: "cfg3.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        interp = "${global.a}"
						        traversal = global.a.b
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				config: runEnvCfg(`
						string = "value"
						number = 666
						list = []
						interp = "${global.a}"
						traversal = global.a.b
				`),
			},
		},
		{
			name: "imported env is merged",
			input: []cfgfile{
				{
					filename: "other/cfg.tm",
					body: `terramate {
						config {
						  run {
							env {
							  string = "value"
							}
						  }
						}
					  }`,
				},
				{
					filename: "cfg1.tm",
					body: `
						import {
							source = "/other/cfg.tm"
						}
					`,
				},
				{
					filename: "cfg2.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        number = 666
						        list = []
						      }
						    }
						  }
						}
					`,
				},
				{
					filename: "cfg3.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        interp = "${global.a}"
						        traversal = global.a.b
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				config: runEnvCfg(`
						string = "value"
						number = 666
						list = []
						interp = "${global.a}"
						traversal = global.a.b
				`),
			},
		},
		{
			name: "redefined env on different env blocks fails",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						      }
						      env {
						        string = "value"
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				errs: []error{errors.E(hcl.ErrTerramateSchema,
					mkrange("cfg.tm", start(9, 15, 147), end(9, 21, 153)))},
			},
		},
		{
			name: "redefined env attribute on different files fails",
			input: []cfgfile{
				{
					filename: "cfg.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						      }
						    }
						  }
						}
					`,
				},
				{
					filename: "cfg2.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						      }
						    }
						  }
						}
					`,
				},
				{
					filename: "cfg3.tm",
					body: `
						terramate {
						  config {
						    run {
						      env {
						        string = "value"
						      }
						    }
						  }
						}
					`,
				},
			},
			want: want{
				errs: []error{
					errors.E(hcl.ErrTerramateSchema,
						mkrange("cfg2.tm", start(6, 15, 84), end(6, 21, 90)),
					),
					errors.E(hcl.ErrTerramateSchema,
						mkrange("cfg3.tm", start(6, 15, 84), end(6, 21, 90)),
					),
				},
			},
		},
	} {
		testParser(t, tc)
	}
}
