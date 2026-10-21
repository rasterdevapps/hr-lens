# HR Lens

Go API and desktop agent, PostgreSQL storage, and an Angular web interface.

- [Backend setup and API documentation](backend/README.md)
- [Windows agent implementation status](backend/cmd/agent/WINDOWS.md)
- [Server deployment guide](backend/deploy/DEPLOY.md)
- [Frontend development](screen-management/README.md)

Windows agent development and validation are described in [Windows deployment and acceptance](docs/WINDOWS.md) and [CI checks](docs/CI.md). A successful build or hosted CI run does not replace interactive desktop acceptance.

This repository contains application source only. Local credentials, release signing keys, captured employee data, dependencies, backups and generated deployment archives are excluded. Production releases require the existing private signing key and the tracked installer templates under `backend/packaging`; CI must not generate or publish a new production signing identity.
