# Contributing to the Go MCP SDK

Thank you for your interest in contributing! The Go SDK needs active
contributions to keep up with changes in the MCP spec, fix bugs, and accommodate
new and emerging use-cases. We welcome all forms of contribution, from filing
and reviewing issues, to contributing fixes, to proposing and implementing new
features.

As described in the [design document](./design/design.md), it is important for
the MCP SDK to remain idiomatic, future-proof, and extensible. The process
described here is intended to ensure that the SDK evolves safely and
transparently, while adhering to these goals.

## Development setup

This module can be built and tested using the standard Go toolchain. Run `go
test ./...` to run all its tests.

To test changes to this module against another module that uses the SDK, we
recommend using a [`go.work` file](https://go.dev/doc/tutorial/workspaces) to
define a multi-module workspace. For example, if your directory contains a
`project` directory containing your project, and a `go-sdk` directory
containing the SDK, run:

```sh
go work init ./project ./go-sdk
```

## Filing issues

This project uses the [GitHub issue
tracker](https://github.com/modelcontextprotocol/go-sdk/issues) for issues. The
process for filing bugs and proposals is described below.

TODO(rfindley): describe a process for asking general questions in the public
MCP discord server.

### Bugs

Please [report
bugs](https://github.com/modelcontextprotocol/go-sdk/issues/new). If the SDK is
not working as you expected, it is likely due to a bug or inadequate
documentation, and reporting an issue will help us address this shortcoming.

When reporting a bug, make sure to answer these five questions:

1. What did you do?
2. What did you see?
3. What did you expect to see?
4. What version of the Go MCP SDK are you using?
5. What version of Go are you using (`go version`)?

### Proposals

A proposal is an issue that proposes a new API for the SDK, or a change to the
signature or behavior of an existing API. Proposals are be labeled with the
'Proposal' label, and require an explicit approval from a maintainer before
being accepted (indicated by the 'Proposal-Accepted' label). Proposals must
remain open for at least a week to allow discussion before being accepted or
declined by a maintainer.

Proposals that are straightforward and uncontroversial may be approved based on
GitHub discussion. However, proposals that are deemed to be sufficiently
unclear or complicated may be deferred to a regular steering meeting (see
'Governance' below).

This process is similar to the [Go proposal
process](https://github.com/golang/proposal), but is necessarily lighter weight
to accommodate the greater rate of change expected for the SDK.

## Contributing code

The project uses GitHub pull requests (PRs) to review changes.

Any significant change should be associated with a GitHub issue. Issues that
are deemed to be good opportunities for contribution are be labeled ['Help
Wanted'](https://github.com/modelcontextprotocol/go-sdk/issues?q=is%3Aissue%20state%3Aopen%20label%3A%22help%20wanted%22).
If you want to work on such an issue, please first comment on the issue to say
that you're interested in contributing. For issues _not_ labeled 'Help Wanted',
it is recommended that you ask (and wait for confirmation) on the issue before
contributing, to avoid duplication of effort or wasted work. For nontrivial
changes that _don't_ relate to an existing issue, please file an issue first.

Changes should be high quality and well tested, and should generally follow the
[Google Go style guide](https://google.github.io/styleguide/go/). Commit
messages should follow the [format used by the Go
project](https://go.dev/wiki/CommitMessage).

Unless otherwise noted, the Go source files are distributed under the MIT-style
license found in the LICENSE file. All Go files in the SDK should have a
copyright header following the format below:

```go
// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
```

## Code of conduct

This project follows the [Go Community Code of Conduct](https://go.dev/conduct).
If you encounter a conduct-related issue, please mail conduct@golang.org.

## Governance

Initially, the Go SDK repository will be administered by the Go team and
Anthropic, and they will be the Approvers (the set of people able to merge PRs
to the SDK). The policies here are also intended to satisfy necessary
constraints of the Go team's participation in the project. This may change in
the future: see 'Ongoing Evaluation' below.

### Steering meetings

On a regular basis, the maintainers will host a virtual steering meeting to
discuss outstanding proposals and other changes to the SDK. These meetings and
their agendas will be announced in advance, and open to all to join. The
meetings will be recorded, and recordings and meeting notes will be made
available afterward. (TODO: decide on a mechanism for tracking these
meetings--likely a GitHub issue.)

This process is similar to the [Go Tools
call](https://go.dev/wiki/golang-tools), though it is expected that meetings
will at least initially occur on a more frequent basis.

### Discord

Discord (either the public or private Anthropic discord servers) should only be
used for logistical coordination or answering questions. For transparency and
durability, design discussion and decisions should occur in GitHub issues or
public steering meetings.

### Antitrust considerations

It is important that the SDK avoids bias toward specific integration paths or
providers. The antitrust policy below details precise terms that should be
avoided to ensure that the evolution and governance of the SDK remain
procompetitive.

A note to readers: this policy was drafted in consultation with counsel, and so
uses terms like 'Steering Committee', which may be confusing in the context of
other 'steering committees' for model context protocol. In the context here,
'Steering Committee' means the set of Approvers, who are able to approve pull
requests and/or make administrative changes to the project.

### Antitrust policy

Note: all changes to the policy in this section must be approved by legal
counsel.

The Go+Anthropic MCP SDK Steering Committee (the “Committee”) is established to
guide and review technical contributions to an open-source Go software
development kit (“SDK”) for the Model Context Protocol (“MCP”). The Committee
and its members are committed to operating for procompetitive purposes that
accelerate AI development and benefit businesses and consumers. This
collaboration is focused on technical and infrastructure objectives – namely,
developing and maintaining a neutral, open-source, and MIT-licensed tool.
Google and Anthropic, as well as other stakeholders, participate with the
understanding that the Committee’s sole purpose is to improve interoperability
and innovation in the MCP ecosystem.

Antitrust law recognizes that when competitors collaborate for valid reasons
(e.g., joint R&D or standard-setting), such collaborations can be
procompetitive. This Antitrust Compliance Policy (the “Policy”) therefore
outlines guidelines and safeguards to ensure the collaboration remains focused
on its technical mission.

The Policy applies to all Committee activities and communications, including
official meetings, subcommittee discussions, emails, shared documents, and any
other interactions under the Committee’s auspices (e.g., group chats, version
control systems). It applies to all participants from Google, Anthropic, and
any other member organizations or independent contributors involved. Each
participating entity should ensure its representatives understand and uphold
these rules. By participating, members agree to follow the Policy in both
letter and spirit.

#### Governance Procedures and Principles

- **Participant Guidelines.** Participants should generally be limited to
  individuals in technical roles who are directly involved in the MCP SDK
  project. These participants should not be key decision-makers in their
  company’s AI commercial strategy, sales, marketing, pricing, or other
  competitively or strategically sensitive business planning.
- **Agenda Preparation.** A written agenda should be circulated before each
  Committee meeting. Agenda items should focus on the SDK’s technical
  development, maintenance, or documentation. Where appropriate, counsel should
  review the agenda prior to circulation to ensure compliance with the Policy.
- **Policy Reminder at Start.** Meetings should begin with a brief antitrust
  compliance reminder with reference to the Policy.
- **Minutes.** Meetings will be minuted by a designated participant, and
  neutrally record attendees, roles, topics discussed, action items, and
  outcomes. Draft minutes will be circulated to all participants.
- **Documentation and Transparency.** Steering Committee outputs are intended
  for public release. Significant design decisions and discussion outcomes
  should be documented publicly. If a topic cannot be safely disclosed
  publicly, it likely does not belong in this forum.
- **Independence of Decision-Making.** All participants and their companies
  retain complete independence in their own business decisions and competitive
  strategies outside of the MCP SDK project. Nothing in this collaboration
  restricts or influences how each company operates its commercial business.

### Information Exchange Guidelines

**Appropriate Topics.** Committee members are anticipated to remain within the
project’s technical scope. In general, discussions should focus on improving
the Go SDK for MCP in a transparent, collaborative, and non-exclusive manner.
The following topics are appropriate and expected:

- **Software Design and Architecture:** Implementation of MCP features in Go,
  API design, code structure, testing frameworks, performance considerations,
  compatibility issues, and security concerns.
- **Technical Contributions and Bug Fixes:** Review of code contributions,
  debugging problems, and handling feature requests.
- **Documentation and Open-Source Logistics:** Discussions of project
  documentation, changelogs, versioning strategy, and managing contributions.
- **Standards and Interoperability:** Ensuring SDK compliance with MCP or other
  open technical standards. Any standardization effort should be open,
  voluntary, and tailored to promote interoperability.
- **Public Information:** Any public information relevant to the Committee’s
  technical work (e.g., published research, open-source code from outside
  projects, publicly documented API specs).

**Inappropriate Topics.** To ensure compliance with antitrust law and maintain
the open character of the collaboration, the following subjects should not be
discussed in Committee meetings, side conversations, or related communications:

- **Pricing and Commercial Terms:** Do not discuss prices, pricing strategy,
  discounting, or future pricing plans for Claude, Gemini, or any other AI
  product or service provided by Committee members.
- **Sales or Output:** Avoid sharing sales volumes, revenue, customer counts,
  market shares, production plans, or any business performance metrics.
- **Product Roadmaps (beyond SDK):** Do not disclose internal plans for AI
  model development, feature rollouts, or future commercialization strategies.
- **Customers or Markets:** No discussions of which customers, industries, or
  geographies the parties will pursue or avoid.
- **Customer or Supplier Details:** Do not share specifics of contracts,
  negotiations, or relationships with commercial partners.
- **Non-SDK Proprietary Tech:** Keep discussions focused on the open SDK.
  Sharing of information should be limited to what is needed to achieve the
  Committee’s goals. Each party’s internal model architectures, fine-tuning
  approaches, or training methods unrelated to the project should not be
  disclosed.
- **HR or Labor Matters:** No discussions about wages, hiring plans, or
  policies toward employees.

#### Enforcement and Support

- **Shared Responsibility.** All Committee participants share responsibility
  for upholding the Policy. While legal counsel can provide support, day-to-day
  compliance is a function of culture and practice.
- **Designated Legal Contacts.** Each participating entity should designate a
  legal point of contact responsible for reviewing meeting materials (e.g.,
  agendas, minutes) and fielding questions about compliance. These contacts
  should be included in the Committee distribution list for all official
  materials and should be consulted in advance of any meetings where sensitive
  topics may arise.
- **Final Note.** The Policy is not meant to chill legitimate technical
  collaboration. It is meant to ensure that the Committee can focus on its
  mission without creating unnecessary legal risk or attracting regulator
  scrutiny. Participants who follow the Policy and avoid Inappropriate Topics
  will remain squarely within the procompetitive zone.

### Ongoing evaluation

On an ongoing basis, the administrators of the SDK will evaluate whether it is
keeping pace with changes to the MCP spec and meeting its goals of openness and
transparency. If it is not meeting these goals, either because it exceeds the
bandwidth of its current Approvers, or because the processes here are
inadequate, these processes will be re-evaluated by the Approvers. At this
time, the Approvers set may be expanded to include additional community
members, based on their history of strong contribution.
