## v0.1.1

- Remove built frontend from the repository (it's now built directly in the GitHub Actions)
- Fix configuration for golangci-lint and enable lint step in the GitHub Actions
- Run `gofmt` for all files in the repository
- Update pocketbase to v0.22.21

## v0.1.0

- Add difference between "Superadmin" and "Editor" roles
  A Editor can only view and edit the content, while a Superadmin can edit the tables and everything else related to the system.
