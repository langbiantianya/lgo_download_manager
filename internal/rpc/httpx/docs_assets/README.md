# Swagger UI assets

The four `.gz` files in this directory are pre-compressed Swagger UI assets from
`github.com/swaggo/files v1.0.1`. Only the files actually served by `/docs/`
are embedded. Keeping them here avoids importing the upstream package, whose
package initialization copies every asset (including source maps and unused
bundles) into an in-memory filesystem in both lgdm processes.

Regenerate an asset by extracting the corresponding byte slice from the
upstream package and compressing it deterministically:

```sh
gzip -9 -n swagger-ui.css
gzip -9 -n swagger-ui-bundle.js
gzip -9 -n swagger-ui-standalone-preset.js
gzip -9 -n favicon-32x32.png
```
