# ShellBeam Server connection page

The default page in `defaultTheme/dist/` is original ShellBeam HTML, CSS, and SVG.
Go embeds these source files directly. `go build` and `go test` need no frontend
checkout, Node.js, downloaded assets, generated archives, or zstd binary.
`PrepareDefaultDist` remains as a startup compatibility check; it does not write
to a cache directory.

This is a connection information page, not a web monitoring or administration
application. Use the ShellBeam client for the user interface. The same page may
be served during first-run installation, migration, or database recovery; its
presence does not indicate that the API is ready for ordinary client use. Follow
the server's logs and the existing setup APIs in those states.

The filename `komari-theme.json`, default theme ID, static routes, and custom
theme support are retained for API compatibility. Existing third-party theme
packages can still be served from `data/theme/<id>/`. Their code and licenses are
separate from the bundled ShellBeam connection page. Restricted startup routers
serve only embedded assets and exclude custom HTML injection, custom themes, and
local favicons.

Edit the committed default assets directly to change this page. Do not copy an
upstream `komari-web` build into this directory or fetch it during a backend build.
