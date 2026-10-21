# HR Lens

Go API and desktop agent, PostgreSQL storage, and an Angular web interface.

- [Backend setup and API documentation](backend/README.md)
- [Windows agent implementation status](backend/cmd/agent/WINDOWS.md)
- [Server deployment guide](backend/deploy/DEPLOY.md)
- [Frontend development](screen-management/README.md)

Windows agent source is present, but capture, recordings and input counts are not implemented yet. A successful Windows build does not establish desktop feature support.

This repository contains application source only. Local credentials, release signing keys, captured employee data, dependencies, backups and generated deployment archives are excluded. Production releases require the existing private signing key and separately maintained installer packaging inputs; CI must not generate or publish a new production signing identity.
