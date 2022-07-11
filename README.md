![](https://raw.githubusercontent.com/mineiros-io/brand/katcipis-add-png-logos/terramate-vertical-logo-black-font-262-120.png#gh-light-mode-only)
![](https://raw.githubusercontent.com/mineiros-io/brand/katcipis-add-png-logos/terramate-vertical-logo-white-font-262-120.png#gh-dark-mode-only)
[<img align="right" src="https://raw.githubusercontent.com/mineiros-io/brand/3bffd30e8bdbbde32c143e2650b2faa55f1df3ea/mineiros-primary-logo.svg" height="23" alt="Mineiros"/>](https://mineiros.io/?ref=terramate)

[![GoDoc](https://pkg.go.dev/badge/github.com/mineiros-io/terramate)](https://pkg.go.dev/github.com/mineiros-io/terramate)
![CI Status](https://github.com/mineiros-io/terramate/actions/workflows/ci.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/mineiros-io/terramate)](https://goreportcard.com/report/github.com/mineiros-io/terramate)
[![codecov](https://codecov.io/gh/mineiros-io/terramate/branch/main/graph/badge.svg?token=gMRUkVUAQ4)](https://codecov.io/gh/mineiros-io/terramate)
[![Join Slack](https://img.shields.io/badge/slack-@mineiros--community-f32752.svg?logo=slack)](https://mineiros.io/slack)

Terramate is a tool for managing multiple Terraform stacks.

The stack concept is not defined by Hashicorp's Terraform tooling but just a convention used by the _Terraform community_, so a stack can be loosely defined as:

> A stack is a runnable Terraform Root Module that operates on a subset of the infrastructure's resources and has its own state.

Terramate provides ways to keep your Terraform code [DRY](https://en.wikipedia.org/wiki/Don%27t_repeat_yourself) and allows to define relationships between stacks and supports you to orchestrate Terraform commands in those stacks with minimal effort to get started in a non-intrusive way.

- **Keep you code DRY**: Avoid duplication by easily sharing data across your project.
- **Code Generation**: Generate valid Terraform Code to ensure that you can always enter a stack to run plain Terraform commands.
- **Stack Change detection**: Only execute commands in stacks that have been changed in the current branch or since the last merge.
- **Module Change detection**: Enhanced Change Detection allows to identifying stacks that have changes in local modules.
- **Execute Any Command**: Terramate is not a wrapper of Terraform but can execute any commands in (changed) stacks.
- **Execution Order**: Explicitly define an order of execution of stacks.
- **Forced Stack Execution**: Ensure specific stacks are run alongside other stacks.
- **Pure HCL**: All configuration of Terramate can be supplied in the well-known [Hashicorp Configuraltion Language (HCL)](https://github.com/hashicorp/hcl).

For more details on how this is achieved, please consider:

* [Why Stacks ?](docs/why-stacks.md)
* [Change Detection](docs/change-detection.md)
* [Config Overview](docs/config-overview.md)
* [Configuring A Project](docs/project-config.md)
* [Sharing Data](docs/sharing-data.md)
* [Code Generation](docs/codegen/overview.md)
* [Orchestrating Stacks Execution](docs/orchestration.md)

If you're interested to know why we decided to build Terramate please consider our blog post:
[Introducing Terramate — An Orchestrator and Code Generator for Terraform](https://medium.com/p/5e538c9ee055).

## Getting Started

### Installing

#### Using Go

To install using Go just run:

```sh
go install github.com/mineiros-io/terramate/cmd/terramate@<version>
```

Where `<version>` is any terramate [version tag](https://github.com/mineiros-io/terramate/tags),
or you can just install the **latest** release:

```sh
go install github.com/mineiros-io/terramate/cmd/terramate@latest
```

#### Using a package manager

- macOS: You can install Terramate on macOS using
  [Homebrew](https://formulae.brew.sh/formula/terramate): `brew install terramate`


#### Using Release Binaries

To install Terramate using a release binary, find the
[appropriate package](https://github.com/mineiros-io/terramate/releases) for
your system and download it.

After downloading Terramate, unzip the package. Terramate runs as a single
binary named `terramate`. Any other files in the package can be safely removed
and Terramate will still function.

Finally, make sure that the `terramate` binary is available on your PATH.
This process will differ depending on your operating system.


#### Using Docker

If you don't want to install Terramate on your host you can use
[Docker](https://www.docker.com/) or [Podman](https://podman.io/) to
run Terramate inside a container:

```sh
docker run ghcr.io/mineiros-io/terramate
```

Container images tagged with release versions are also provided.
Click [here](https://github.com/mineiros-io/terramate/pkgs/container/terramate/versions)
for a list of the available container image tags.


#### Auto Completion

Terramate supports autocompletion of commands for *bash*, *zsh* and *fish*. To
install the completion just run the command below and open a new shell session:

```sh
terramate install-completions
```

### Project Setup

If you already have a project versioned on Git setting up
Terramate is as easy as just [installing Terramate](#installing).
Terramate comes with sensible defaults so just using it inside a pre existent
Git repository should not require any configurations.

The exception being repositories that have a default remote branch
other than `origin/main`, in that case to make change detection work you will
need to set a customized [project configuration](docs/project-config.md).

If you want to play around with Terramate from scratch locally you can also
setup a local git repository:

```sh
playground=$(mktemp -d)
local_origin=$(mktemp -d)

git init -b main "${playground}"
git init -b main "${local_origin}"  --bare

cd "${playground}"
git remote add origin "${local_origin}"

echo "My Terramate Playground" > README.md

git add README.md
git commit -m "first commit"
git push --set-upstream origin main

# Start using terramate
```

Terramate can also work without any VCS setup, it will only require
a Terramate configuration at the top level directory of the project

```sh
playground=$(mktemp -d)
cd "${playground}"

cat > terramate.tm.hcl <<- EOM
terramate {
  config {
  }
}
EOM

# Start using terramate
```

In a setup with no VCS change detection features will not be available.

You can also check our [live example](https://github.com/mineiros-io/terramate-example-code-generation).
