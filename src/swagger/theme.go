package swagger

// CSSVariables returns Swagger role aliases that follow the shared palette.
func CSSVariables() string {
	return `html.theme-dark,
html.theme-light,
html.theme-auto {
  --swagger-bg: var(--color-bg-primary);
  --swagger-bg-alt: var(--color-bg-tertiary);
  --swagger-bg-elevated: var(--color-bg-secondary);
  --swagger-text: var(--color-text-primary);
  --swagger-muted: var(--color-text-secondary);
  --swagger-border: var(--color-border);
  --swagger-link: var(--color-info);
  --swagger-get: var(--color-info);
  --swagger-post: var(--color-success);
  --swagger-put: var(--color-warning);
  --swagger-delete: var(--color-error);
  --swagger-purple: var(--color-accent-primary);
}
`
}
