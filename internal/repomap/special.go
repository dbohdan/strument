package repomap

import (
	"path"
	"strings"
)

// rootImportantFiles is aider special.py's ROOT_IMPORTANT_FILES.
// Matching is exact against the normalized
// root-relative path, plus the single glob special-case for GitHub Actions
// workflow .yml files.
var rootImportantFiles = []string{
	// Version Control
	".gitignore",
	".gitattributes",
	// Documentation
	"README",
	"README.md",
	"README.txt",
	"README.rst",
	"CONTRIBUTING",
	"CONTRIBUTING.md",
	"CONTRIBUTING.txt",
	"CONTRIBUTING.rst",
	"LICENSE",
	"LICENSE.md",
	"LICENSE.txt",
	"CHANGELOG",
	"CHANGELOG.md",
	"CHANGELOG.txt",
	"CHANGELOG.rst",
	"SECURITY",
	"SECURITY.md",
	"SECURITY.txt",
	"CODEOWNERS",
	// Package Management and Dependencies
	"requirements.txt",
	"Pipfile",
	"Pipfile.lock",
	"pyproject.toml",
	"setup.py",
	"setup.cfg",
	"package.json",
	"package-lock.json",
	"yarn.lock",
	"npm-shrinkwrap.json",
	"Gemfile",
	"Gemfile.lock",
	"composer.json",
	"composer.lock",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"build.sbt",
	"go.mod",
	"go.sum",
	"Cargo.toml",
	"Cargo.lock",
	"mix.exs",
	"rebar.config",
	"project.clj",
	"Podfile",
	"Cartfile",
	"dub.json",
	"dub.sdl",
	// Configuration and Settings
	".env",
	".env.example",
	".editorconfig",
	"tsconfig.json",
	"jsconfig.json",
	".babelrc",
	"babel.config.js",
	".eslintrc",
	".eslintignore",
	".prettierrc",
	".stylelintrc",
	"tslint.json",
	".pylintrc",
	".flake8",
	".rubocop.yml",
	".scalafmt.conf",
	".dockerignore",
	".gitpod.yml",
	"sonar-project.properties",
	"renovate.json",
	"dependabot.yml",
	".pre-commit-config.yaml",
	"mypy.ini",
	"tox.ini",
	".yamllint",
	"pyrightconfig.json",
	// Build and Compilation
	"webpack.config.js",
	"rollup.config.js",
	"parcel.config.js",
	"gulpfile.js",
	"Gruntfile.js",
	"build.xml",
	"build.boot",
	"project.json",
	"build.cake",
	"MANIFEST.in",
	// Testing
	"pytest.ini",
	"phpunit.xml",
	"karma.conf.js",
	"jest.config.js",
	"cypress.json",
	".nycrc",
	".nycrc.json",
	// CI/CD
	".travis.yml",
	".gitlab-ci.yml",
	"Jenkinsfile",
	"azure-pipelines.yml",
	"bitbucket-pipelines.yml",
	"appveyor.yml",
	"circle.yml",
	".circleci/config.yml",
	".github/dependabot.yml",
	"codecov.yml",
	".coveragerc",
	// Docker and Containers
	"Dockerfile",
	"docker-compose.yml",
	"docker-compose.override.yml",
	// Cloud and Serverless
	"serverless.yml",
	"firebase.json",
	"now.json",
	"netlify.toml",
	"vercel.json",
	"app.yaml",
	"terraform.tf",
	"main.tf",
	"cloudformation.yaml",
	"cloudformation.json",
	"ansible.cfg",
	"kubernetes.yaml",
	"k8s.yaml",
	// Database
	"schema.sql",
	"liquibase.properties",
	"flyway.conf",
	// Framework-specific
	"next.config.js",
	"nuxt.config.js",
	"vue.config.js",
	"angular.json",
	"gatsby-config.js",
	"gridsome.config.js",
	// API Documentation
	"swagger.yaml",
	"swagger.json",
	"openapi.yaml",
	"openapi.json",
	// Development environment
	".nvmrc",
	".ruby-version",
	".python-version",
	"Vagrantfile",
	// Quality and metrics
	".codeclimate.yml",
	"codecov.yml",
	// Documentation
	"mkdocs.yml",
	"_config.yml",
	"book.toml",
	"readthedocs.yml",
	".readthedocs.yaml",
	// Package registries
	".npmrc",
	".yarnrc",
	// Linting and formatting
	".isort.cfg",
	".markdownlint.json",
	".markdownlint.yaml",
	// Security
	".bandit",
	".secrets.baseline",
	// Misc
	".pypirc",
	".gitkeep",
	".npmignore",
}

var normalizedImportantFiles = func() map[string]bool {
	m := make(map[string]bool, len(rootImportantFiles))
	for _, p := range rootImportantFiles {
		m[path.Clean(p)] = true
	}
	return m
}()

// isImportant ports special.is_important over forward-slashed rel paths.
func isImportant(filePath string) bool {
	fileName := path.Base(filePath)
	dirName := path.Clean(path.Dir(filePath))
	if dirName == ".github/workflows" && strings.HasSuffix(fileName, ".yml") {
		return true
	}
	return normalizedImportantFiles[path.Clean(filePath)]
}

// filterImportantFiles keeps the important paths, preserving input order.
func filterImportantFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		if isImportant(p) {
			out = append(out, p)
		}
	}
	return out
}
