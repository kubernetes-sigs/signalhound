# signalhound

Hunts Flake And Failing Testgrid Jobs

Signalhound monitors TestGrid dashboards to identify and summarize test failures and flaking patterns in Kubernetes
CI/CD pipelines. It provides actionable insights for CI signal enumeration, currently focusing on
`sig-release-master-blocking` and `sig-release-master-informing` dashboards.

 <img src="assets/img/signalhound_abstract_01.png" title="signalhound tui for kubernetes release test analysis" />

## Features

### 📊 Test Monitoring Dashboard

Run Signalhound with the `abstract` command to launch an interactive text user interface (TUI) that displays:

* Board#Tabs combinations in the first panel for easy navigation
* Test listings when selecting specific board combinations
*  Dual information panels:
** Left panel: Slack summary from #release-ci-signal channel (Markdown formatted)
** Right panel: GitHub issue template with Kubernetes defaults (Markdown formatted)

### 📋 Draft issues automatically in the CI Signal Board
Access drafts in the DRAFTING section after selecting a panel and pressing Ctrl-B
Configure with a Personal Access Token (PAT) with appropriate repository permissions

* Clipboard Integration

Press Ctrl-Space on any panel to copy content to clipboard
Currently optimized for WSL2 environments

## Installation and Build

Prerequisites

* Go 1.24 or later
* Git
* Github Token PAT
* Kubernetes cluster (kind)

### Prerequisites

To support the ability to `automatically create issues` in the Kubernetes GitHub
repository, signalhound requires a [GitHub PAT (personal access token)](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens). Once
you create a PAT you will store it in the local `SIGNALHOUND_GITHUB_TOKEN` or `GITHUB_TOKEN` variable
so that signalhound can access it.  For instructions to create a GitHub PAT,
[visit the docs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens#creating-a-fine-grained-personal-access-token).

```bash
# Best practice is to use a fine-grained personal access token
# specifically created for signalhound.
# It is best to not share tokens across applications.
export SIGNALHOUND_GITHUB_TOKEN=<github.pat.for.signalhound>
# If you prefer you may fall back to the generic github token
# var that is well-known and may be read by other binaries.
export GITHUB_TOKEN=<github.pat.default>
```

### Running at runtime

```bash
git clone https://github.com/kubernetes-sigs/signalhound.git
cd signalhound
make run  # for abstract and standalone
make run-controller # for running the controller outside the cluster
```

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/signalhound:<tag>
```

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/signalhound:<tag>
```

**Create instances of your solution**

You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

### To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Support

Create an issue on GitHub for bug reports and feature requests
* Join the #sig-release channel in Kubernetes Slack for community support
* Check the documentation for detailed usage guides

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

## Community, discussion, contribution, and support

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

You can reach the maintainers of this project at:

- [Slack channel](https://kubernetes.slack.com/messages/sig-release)
- [Mailing List](https://groups.google.com/a/kubernetes.io/g/sig-release)

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).

