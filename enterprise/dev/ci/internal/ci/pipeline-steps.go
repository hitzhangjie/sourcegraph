package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sourcegraph/sourcegraph/enterprise/dev/ci/images"
	bk "github.com/sourcegraph/sourcegraph/enterprise/dev/ci/internal/buildkite"
)

// Verifies the docs formatting and builds the `docsite` command.
func addDocs(pipeline *bk.Pipeline) {
	pipeline.AddStep(":memo: Check and build docsite",
		bk.Cmd("./dev/ci/yarn-run.sh prettier-check"),
		bk.Cmd("./dev/check/docsite.sh"))
}

// Adds the static check test step.
func addCheck(pipeline *bk.Pipeline) {
	pipeline.AddStep(":white_check_mark: Misc Linters", bk.Cmd("./dev/check/all.sh"))
}

// Adds the lint test step.
func addLint(pipeline *bk.Pipeline) {
	// If we run all lints together it is our slow step (5m). So we split it
	// into two and try balance the runtime. yarn is a fixed cost so we always
	// pay it on a step. Aim for around 3m.
	//
	// Random sample of timings:
	//
	// - yarn 41s
	// - eslint 137s
	// - build-ts 60s
	// - prettier 29s
	// - stylelint 7s
	// - graphql-lint 1s
	pipeline.AddStep(":eslint: Lint all Typescript",
		bk.Cmd("dev/ci/yarn-run.sh build-ts all:eslint")) // eslint depends on build-ts
	pipeline.AddStep(":lipstick: :lint-roller: :stylelint: :graphql:", // TODO: Add header - Similar to the previous step
		bk.Cmd("dev/ci/yarn-run.sh prettier-check all:stylelint graphql-lint"))
}

// Adds steps for the OSS and Enterprise web app builds. Runs the web app tests.
func addWebApp(pipeline *bk.Pipeline) {
	// Webapp build
	pipeline.AddStep(":webpack::globe_with_meridians: Build",
		bk.Cmd("dev/ci/yarn-build.sh client/web"),
		bk.Env("NODE_ENV", "production"),
		bk.Env("ENTERPRISE", ""))

	// Webapp enterprise build
	pipeline.AddStep(":webpack::globe_with_meridians::moneybag: Enterprise build",
		bk.Cmd("dev/ci/yarn-build.sh client/web"),
		bk.Env("NODE_ENV", "production"),
		bk.Env("ENTERPRISE", "1"))

	// Webapp tests
	pipeline.AddStep(":jest::globe_with_meridians: Test",
		bk.Cmd("dev/ci/yarn-test.sh client/web"),
		bk.Cmd("dev/ci/codecov.sh -c -F typescript -F unit"))
}

// Don't download browser, we use "download-puppeteer-browser" script instead
var percyBrowserExecutableEnv = bk.Env("PERCY_BROWSER_EXECUTABLE", "node_modules/puppeteer/.local-chromium/linux-885174/chrome-linux/chrome")

// Builds and tests the browser extension.
func addBrowserExt(pipeline *bk.Pipeline) {
	// Browser extension integration tests
	for _, browser := range []string{"chrome"} {
		pipeline.AddStep(
			fmt.Sprintf(":%s: Puppeteer tests for %s extension", browser, browser),
			percyBrowserExecutableEnv,
			bk.Env("EXTENSION_PERMISSIONS_ALL_URLS", "true"),
			bk.Env("BROWSER", browser),
			bk.Env("LOG_BROWSER_CONSOLE", "true"),
			bk.Env("SOURCEGRAPH_BASE_URL", "https://sourcegraph.com"),
			bk.Env("RECORD", "false"), // ensure that we use existing recordings
			bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
			bk.Cmd("yarn --cwd client/shared run download-puppeteer-browser"),
			bk.Cmd("yarn --cwd client/browser -s run build"),
			bk.Cmd("yarn run cover-browser-integration"),
			bk.Cmd("yarn nyc report -r json"),
			bk.Cmd("dev/ci/codecov.sh -c -F typescript -F integration"),
			bk.ArtifactPaths("./puppeteer/*.png"),
		)
	}

	// Browser extension unit tests
	pipeline.AddStep(":jest::chrome: Test browser extension",
		bk.Cmd("dev/ci/yarn-test.sh client/browser"),
		bk.Cmd("dev/ci/codecov.sh -c -F typescript -F unit"))
}

func addClientIntegrationTests(pipeline *bk.Pipeline) {
	CHUNK_SIZE := 3
	PREP_STEP_KEY := "puppeteer:prep"
	SKIP_GIT_CLONE_STEP := bk.Plugin("uber-workflow/run-without-clone", "")

	// Build web application used for integration tests to share it between multiple parallel steps.
	pipeline.AddStep(":puppeteer::electric_plug: Puppeteer tests prep",
		bk.Key(PREP_STEP_KEY),
		bk.Env("ENTERPRISE", "1"),
		bk.Cmd("COVERAGE_INSTRUMENT=true dev/ci/yarn-build.sh client/web"),
		bk.Cmd("dev/ci/create-client-artifact.sh"))

	// Chunk web integration tests to save time via parallel execution.
	chunkedTestFiles := getChunkedWebIntegrationFileNames(CHUNK_SIZE)
	// Percy finalize step should be executed after all integration tests.
	puppeteerFinalizeDependencies := make([]bk.StepOpt, len(chunkedTestFiles))

	// Add pipeline step for each chunk of web integrations files.
	for i, chunkTestFiles := range chunkedTestFiles {
		stepLabel := fmt.Sprintf(":puppeteer::electric_plug: Puppeteer tests chunk #%s", fmt.Sprint(i+1))

		stepKey := fmt.Sprintf("puppeteer:chunk:%s", fmt.Sprint(i+1))
		puppeteerFinalizeDependencies[i] = bk.DependsOn(stepKey)

		pipeline.AddStep(stepLabel,
			SKIP_GIT_CLONE_STEP,
			bk.Key(stepKey),
			bk.DependsOn(PREP_STEP_KEY),
			percyBrowserExecutableEnv,
			bk.Env("PERCY_ON", "true"),
			bk.Cmd(fmt.Sprintf(`dev/ci/yarn-web-integration.sh "%s"`, chunkTestFiles)),
			bk.ArtifactPaths("./puppeteer/*.png"))
	}

	finalizeSteps := []bk.StepOpt{
		SKIP_GIT_CLONE_STEP,
		bk.Cmd("npx @percy/cli build:finalize"),
	}

	pipeline.AddStep(":puppeteer::electric_plug: Puppeteer tests finalize",
		append(finalizeSteps, puppeteerFinalizeDependencies...)...)
}

func addClientLighthouseTests(pipeline *bk.Pipeline) {
	PREP_STEP_KEY := "lighthouse:prep"

	// Build web application used for integration tests to share it between multiple parallel steps.
	pipeline.AddStep(":lighthouse: Lighthouse production build",
		bk.Key(PREP_STEP_KEY),
		bk.Env("NODE_ENV", "production"),
		bk.Env("WEBPACK_SERVE_INDEX", "true"), // Required for local production server
		bk.Env("SOURCEGRAPH_API_URL", "https://sourcegraph.com"),
		bk.Cmd("dev/ci/yarn-build.sh client/web"),
		bk.Cmd("dev/ci/create-client-artifact.sh"))

	testPaths := map[string]string{
		"homepage":        "/",
		"search_results":  "/search?q=repo:sourcegraph/sourcegraph+file:package.json",
		"repository_page": "/github.com/sourcegraph/sourcegraph",
		"file_blob":       "/github.com/sourcegraph/sourcegraph/-/blob/package.json",
	}

	for key, path := range testPaths {
		stepLabel := fmt.Sprintf(":lighthouse: lighthouse:%s", key)
		pipeline.AddStep(stepLabel,
			bk.Key(key),
			bk.Env("SOURCEGRAPH_API_URL", "https://sourcegraph.com"),
			bk.Cmd(fmt.Sprintf(`dev/ci/yarn-lighthouse.sh %s %s`, key, path)),
			bk.DependsOn(PREP_STEP_KEY))
	}
}

func addChromaticTests(c Config, pipeline *bk.Pipeline) {
	// Upload storybook to Chromatic
	chromaticCommand := "yarn chromatic --exit-zero-on-changes --exit-once-uploaded"

	if c.isMainBranch() {
		chromaticCommand += " --auto-accept-changes"
	}

	pipeline.AddStep(":chromatic: Upload Storybook to Chromatic",
		bk.AutomaticRetry(5),
		bk.Cmd("yarn --mutex network --frozen-lockfile --network-timeout 60000"),
		bk.Cmd("yarn gulp generate"),
		bk.Env("MINIFY", "1"),
		bk.Cmd(chromaticCommand))

}

// Adds the shared frontend tests (shared between the web app and browser extension).
func addSharedTests(c Config) func(pipeline *bk.Pipeline) {
	return func(pipeline *bk.Pipeline) {
		if c.isMainDryRun || c.isClientAffected() {
			addClientIntegrationTests(pipeline)
			addClientLighthouseTests(pipeline)
			addChromaticTests(c, pipeline)
		}

		// Shared tests
		pipeline.AddStep(":jest: Test shared client code",
			bk.Cmd("dev/ci/yarn-test.sh client/shared"),
			bk.Cmd("dev/ci/codecov.sh -c -F typescript -F unit"))

		// Wildcard tests
		pipeline.AddStep(":jest: Test wildcard client code",
			bk.Cmd("dev/ci/yarn-test.sh client/wildcard"),
			bk.Cmd("dev/ci/codecov.sh -c -F typescript -F unit"))
	}
}

func addBrandedTests(pipeline *bk.Pipeline) {
	pipeline.AddStep(":jest: Test branded client code",
		bk.Cmd("dev/ci/yarn-test.sh client/branded"),
		bk.Cmd("dev/ci/codecov.sh -c -F typescript -F unit"))
}

// Adds PostgreSQL backcompat tests.
func addPostgresBackcompat(pipeline *bk.Pipeline) {
	// TODO: We do not test Postgres DB backcompat anymore.
}

// Adds the Go test step.
func addGoTests(pipeline *bk.Pipeline) {
	pipeline.AddStep(":go: Test",
		bk.Cmd("./dev/ci/go-test.sh"),
		bk.Cmd("dev/ci/codecov.sh -c -F go"))
}

// Builds the OSS and Enterprise Go commands.
func addGoBuild(pipeline *bk.Pipeline) {
	pipeline.AddStep(":go: Build",
		bk.Cmd("./dev/ci/go-build.sh"),
	)
}

// Lints the Dockerfiles.
func addDockerfileLint(pipeline *bk.Pipeline) {
	pipeline.AddStep(":docker: Lint",
		bk.Cmd("./dev/ci/docker-lint.sh"))
}

// Adds backend integration tests step.
func addBackendIntegrationTests(c Config) func(*bk.Pipeline) {
	return func(pipeline *bk.Pipeline) {
		if !c.isBackendDryRun && !c.isMainDryRun && c.branch != "master" && !c.isMainBranch() {
			return
		}

		pipeline.AddStep(":chains: Backend integration tests",
			bk.Cmd("pushd enterprise"),
			bk.Cmd("./cmd/server/pre-build.sh"),
			bk.Cmd("./cmd/server/build.sh"),
			bk.Cmd("popd"),
			bk.Cmd("./dev/ci/backend-integration.sh"),
			bk.Cmd(`docker image rm -f "$IMAGE"`),
		)
	}
}

func addBrowserExtensionE2ESteps(pipeline *bk.Pipeline) {
	for _, browser := range []string{"chrome"} {
		// Run e2e tests
		pipeline.AddStep(fmt.Sprintf(":%s: E2E for %s extension", browser, browser),
			percyBrowserExecutableEnv,
			bk.Env("EXTENSION_PERMISSIONS_ALL_URLS", "true"),
			bk.Env("BROWSER", browser),
			bk.Env("LOG_BROWSER_CONSOLE", "true"),
			bk.Env("SOURCEGRAPH_BASE_URL", "https://sourcegraph.com"),
			bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
			bk.Cmd("yarn --cwd client/shared run download-puppeteer-browser"),
			bk.Cmd("pushd client/browser"),
			bk.Cmd("yarn -s run build"),
			bk.Cmd("yarn -s mocha ./src/end-to-end/github.test.ts ./src/end-to-end/gitlab.test.ts"),
			bk.Cmd("popd"),
			bk.ArtifactPaths("./puppeteer/*.png"))
	}
}

// Release the browser extension.
func addBrowserExtensionReleaseSteps(pipeline *bk.Pipeline) {
	addBrowserExtensionE2ESteps(pipeline)

	pipeline.AddWait()

	// Release to the Chrome Webstore
	pipeline.AddStep(":rocket::chrome: Extension release",
		bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
		bk.Cmd("pushd client/browser"),
		bk.Cmd("yarn -s run build"),
		bk.Cmd("yarn release:chrome"),
		bk.Cmd("popd"))

	// Build and self sign the FF add-on and upload it to a storage bucket
	pipeline.AddStep(":rocket::firefox: Extension release",
		bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
		bk.Cmd("pushd client/browser"),
		bk.Cmd("yarn release:ff"),
		bk.Cmd("popd"))

	// Release to npm
	pipeline.AddStep(":rocket::npm: NPM Release",
		bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
		bk.Cmd("pushd client/browser"),
		bk.Cmd("yarn -s run build"),
		bk.Cmd("yarn release:npm"),
		bk.Cmd("popd"))
}

// Adds a Buildkite pipeline "Wait".
func wait(pipeline *bk.Pipeline) {
	pipeline.AddWait()
}

// Trigger the async pipeline to run.
func triggerAsync(c Config) func(*bk.Pipeline) {
	env := copyEnv(
		"BUILDKITE_PULL_REQUEST",
		"BUILDKITE_PULL_REQUEST_BASE_BRANCH",
		"BUILDKITE_PULL_REQUEST_REPO",
	)

	return func(pipeline *bk.Pipeline) {
		pipeline.AddTrigger(":snail: Trigger Async",
			bk.Trigger("sourcegraph-async"),
			bk.Async(true),
			bk.Build(bk.BuildOptions{
				Message: os.Getenv("BUILDKITE_MESSAGE"),
				Commit:  c.commit,
				Branch:  c.branch,
				Env:     env,
			}),
		)
	}
}

func triggerUpdaterPipeline(c Config) func(*bk.Pipeline) {
	if !c.isMainBranch() {
		// no-op
		return func(*bk.Pipeline) {}
	}

	return func(pipeline *bk.Pipeline) {
		pipeline.AddStep(":github: :date: :k8s: Trigger k8s updates if current commit is tip of 'main'",
			bk.Cmd(".buildkite/updater/trigger-if-tip-of-main.sh"),
			bk.Concurrency(1),
			bk.ConcurrencyGroup("sourcegraph/sourcegraph-k8s-update-trigger"),
		)
	}
}

// images used by cluster-qa test
func clusterDockerImages(images []string) string {
	var clusterImages []string
	imagesToRemove := map[string]bool{"server": true, "ignite-ubuntu": true}
	for _, image := range images {
		if _, exists := imagesToRemove[image]; !exists {
			clusterImages = append(clusterImages, image)
		}
	}
	return strings.Join(clusterImages, "\n")
}

func triggerE2EandQA(c Config, commonEnv map[string]string) func(*bk.Pipeline) {
	var async bool
	if c.isMainBranch() {
		async = true
	} else {
		async = false
	}

	env := copyEnv(
		"BUILDKITE_PULL_REQUEST",
		"BUILDKITE_PULL_REQUEST_BASE_BRANCH",
		"BUILDKITE_PULL_REQUEST_REPO",
	)
	env["COMMIT_SHA"] = commonEnv["COMMIT_SHA"]
	env["DATE"] = commonEnv["DATE"]
	env["VERSION"] = commonEnv["VERSION"]
	env["CI_DEBUG_PROFILE"] = commonEnv["CI_DEBUG_PROFILE"]

	// Set variables that indicate the tag for 'us.gcr.io/sourcegraph-dev' images built
	// from this CI run's commit, and credentials to access them.
	env["CANDIDATE_VERSION"] = c.candidateImageTag()
	env["VAGRANT_SERVICE_ACCOUNT"] = "buildkite@sourcegraph-ci.iam.gserviceaccount.com"

	// Test upgrades from mininum upgradeable Sourcegraph version - updated by release tool
	env["MINIMUM_UPGRADEABLE_VERSION"] = "3.31.2"

	env["DOCKER_CLUSTER_IMAGES_TXT"] = clusterDockerImages(images.SourcegraphDockerImages)

	return func(pipeline *bk.Pipeline) {
		if !c.shouldRunE2EandQA() {
			return
		}

		pipeline.AddTrigger(":chromium: Trigger E2E",
			bk.Trigger("sourcegraph-e2e"),
			bk.Async(async),
			bk.Build(bk.BuildOptions{
				Message: os.Getenv("BUILDKITE_MESSAGE"),
				Commit:  c.commit,
				Branch:  c.branch,
				Env:     env,
			}),
		)
		pipeline.AddTrigger(":chromium: Trigger QA",
			bk.Trigger("qa"),
			bk.Async(async),
			bk.Build(bk.BuildOptions{
				Message: os.Getenv("BUILDKITE_MESSAGE"),
				Commit:  c.commit,
				Branch:  c.branch,
				Env:     env,
			}),
		)
		pipeline.AddTrigger(":chromium: Trigger Code Intel QA",
			bk.Trigger("code-intel-qa"),
			bk.Async(async),
			bk.Build(bk.BuildOptions{
				Message: os.Getenv("BUILDKITE_MESSAGE"),
				Commit:  c.commit,
				Branch:  c.branch,
				Env:     env,
			}),
		)
	}
}

func copyEnv(keys ...string) map[string]string {
	m := map[string]string{}
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	return m
}

// Build all relevant Docker images for Sourcegraph (for example, candidates and final
// images), given the current CI case (e.g., "tagged release", "release branch",
// "master branch", etc.)
//
// Notes:
//
// - Publishing of `insiders` implies deployment
// - See `images.go` for more details on what images get built and where they get published
func addDockerImages(c Config, final bool) func(*bk.Pipeline) {
	addDockerImage := func(c Config, app string, insiders bool) func(*bk.Pipeline) {
		if !final {
			return addCandidateDockerImage(c, app)
		}
		return addFinalDockerImage(c, app, insiders)
	}

	return func(pipeline *bk.Pipeline) {
		switch {
		// build candidate images and deploy `insiders` images
		case c.isMainBranch():
			for _, dockerImage := range images.SourcegraphDockerImages {
				addDockerImage(c, dockerImage, true)(pipeline)
			}

		// build candidate images but do not deploy `insiders` images
		case c.taggedRelease || c.isBackendDryRun || c.shouldRunE2EandQA() || c.buildCandidatesNoTest:
			for _, dockerImage := range images.SourcegraphDockerImages {
				addDockerImage(c, dockerImage, false)(pipeline)
			}

		// only build candidate image for the specified image in the branch name
		// see https://about.sourcegraph.com/handbook/engineering/deployments/testing#building-docker-images-for-a-specific-branch
		case strings.HasPrefix(c.branch, "docker-images-patch/"):
			addDockerImage(c, c.branch[20:], false)(pipeline)
		}
	}
}

// Build a candidate docker image that will re-tagged with the final
// tags once the e2e tests pass.
func addCandidateDockerImage(c Config, app string) func(*bk.Pipeline) {
	return func(pipeline *bk.Pipeline) {
		image := strings.ReplaceAll(app, "/", "-")
		localImage := "sourcegraph/" + image + ":" + c.version

		cmds := []bk.StepOpt{
			bk.Cmd(fmt.Sprintf(`echo "Building candidate %s image..."`, app)),
			bk.Env("DOCKER_BUILDKIT", "1"),
			bk.Env("IMAGE", localImage),
			bk.Env("VERSION", c.version),
			bk.Cmd("yes | gcloud auth configure-docker"),
		}

		if _, err := os.Stat(filepath.Join("docker-images", app)); err == nil {
			// Building Docker image located under $REPO_ROOT/docker-images/
			cmds = append(cmds, bk.Cmd(filepath.Join("docker-images", app, "build.sh")))
		} else {
			// Building Docker images located under $REPO_ROOT/cmd/
			cmdDir := func() string {
				if _, err := os.Stat(filepath.Join("enterprise/cmd", app)); err != nil {
					fmt.Fprintf(os.Stderr, "github.com/sourcegraph/sourcegraph/enterprise/cmd/%s does not exist so building github.com/sourcegraph/sourcegraph/cmd/%s instead\n", app, app)
					return "cmd/" + app
				}
				return "enterprise/cmd/" + app
			}()
			preBuildScript := cmdDir + "/pre-build.sh"
			if _, err := os.Stat(preBuildScript); err == nil {
				cmds = append(cmds, bk.Cmd(preBuildScript))
			}
			cmds = append(cmds, bk.Cmd(cmdDir+"/build.sh"))
		}

		devImage := fmt.Sprintf("%s/%s", images.SourcegraphDockerDevRegistry, image)
		devTag := c.candidateImageTag()
		cmds = append(cmds,
			// Retag the local image for dev registry
			bk.Cmd(fmt.Sprintf("docker tag %s %s:%s", localImage, devImage, devTag)),
			// Publish tagged image
			bk.Cmd(fmt.Sprintf("docker push %s:%s", devImage, devTag)),
		)

		pipeline.AddStep(fmt.Sprintf(":docker: :construction: %s", app), cmds...)
	}
}

var currentBuildTimestamp = strconv.Itoa(int(time.Now().UTC().Unix()))

func addExecutorPackerStep(c Config, final bool) func(*bk.Pipeline) {
	return func(pipeline *bk.Pipeline) {
		if !c.isMainBranch() && !c.isMainDryRun {
			return
		}

		if final {
			if !c.isMainDryRun {
				cmds := []bk.StepOpt{
					bk.Cmd(`echo "Releasing executor cloud image..."`),
					bk.Env("VERSION", c.version),
					bk.Env("BUILD_TIMESTAMP", currentBuildTimestamp),
					bk.Cmd("./enterprise/cmd/executor/release.sh"),
				}

				pipeline.AddStep(":packer: :white_check_mark: executor image", cmds...)
			}
		} else {
			cmds := []bk.StepOpt{
				bk.Cmd(`echo "Building executor cloud image..."`),
				bk.Env("VERSION", c.version),
				bk.Env("BUILD_TIMESTAMP", currentBuildTimestamp),
				bk.Cmd("./enterprise/cmd/executor/build.sh"),
			}

			pipeline.AddStep(":packer: :construction: executor image", cmds...)
		}
	}
}

// Tag and push final Docker image for the service defined by `app`
// after the e2e tests pass.
func addFinalDockerImage(c Config, app string, insiders bool) func(*bk.Pipeline) {
	return func(pipeline *bk.Pipeline) {
		image := strings.ReplaceAll(app, "/", "-")
		devImage := fmt.Sprintf("%s/%s", images.SourcegraphDockerDevRegistry, image)
		publishImage := fmt.Sprintf("%s/%s", images.SourcegraphDockerPublishRegistry, image)

		var images []string
		for _, image := range []string{publishImage, devImage} {
			if app != "server" || c.taggedRelease || c.patch || c.patchNoTest {
				images = append(images, fmt.Sprintf("%s:%s", image, c.version))
			}

			if app == "server" && c.releaseBranch {
				images = append(images, fmt.Sprintf("%s:%s-insiders", image, c.branch))
			}

			if insiders {
				images = append(images, fmt.Sprintf("%s:insiders", image))
			}
		}

		// these tags are pushed to our dev registry, and are only
		// used internally
		for _, tag := range []string{
			c.version,
			c.commit,
			c.shortCommit(),
			fmt.Sprintf("%s_%s_%d", c.shortCommit(), c.now.Format("2006-01-02"), c.buildNumber),
			fmt.Sprintf("%s_%d", c.shortCommit(), c.buildNumber),
			fmt.Sprintf("%s_%d", c.commit, c.buildNumber),
			strconv.Itoa(c.buildNumber),
		} {
			internalImage := fmt.Sprintf("%s:%s", devImage, tag)
			images = append(images, internalImage)
		}

		candidateImage := fmt.Sprintf("%s:%s", devImage, c.candidateImageTag())
		cmd := fmt.Sprintf("./dev/ci/docker-publish.sh %s %s", candidateImage, strings.Join(images, " "))

		pipeline.AddStep(fmt.Sprintf(":docker: :white_check_mark: %s", app), bk.Cmd(cmd))
	}
}
