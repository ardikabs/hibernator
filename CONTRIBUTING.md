# Contributing to Hibernator

Thank you for your interest in contributing to Hibernator! We welcome contributions from the community to help improve and expand this project.

## Development

### Prerequisites

- Go 1.24+
- `make`
- A Kubernetes cluster (for testing)

### Build & Test

```bash
# Build controller
make build

# Build runner
make build-runner

# Run unit tests
make test

# Run E2E tests (full hibernation cycle)
make test-e2e

# Run linter
make lint
```

### Local Development

```bash
# Install CRDs
make install

# Run controller locally
make run

# Run tests with coverage
make test-coverage
```

## How to Contribute

1. **Read the RFCs**: Start with [`enhancements/0001-hibernate-operator.md`](enhancements/0001-hibernate-operator.md) for project architecture.
2. **Discuss first**: Open an issue for major changes before implementation to ensure alignment with the project's goals.
3. **Write tests**: Add unit tests for all new code and integration tests for new features.
4. **Update docs**: Keep the `README.md`, `USAGE.md`, and relevant RFCs synchronized with your changes.
5. **Submit a Pull Request**: Ensure your code passes all linting and test checks before submitting.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
