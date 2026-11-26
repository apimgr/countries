(() => {
  const mount = document.getElementById('swagger-ui');
  if (!mount || typeof window.SwaggerUIBundle !== 'function') return;

  window.swaggerUI = window.SwaggerUIBundle({
    domNode: mount,
    url: mount.dataset.specUrl,
    deepLinking: true,
    displayRequestDuration: true,
    docExpansion: 'list',
    layout: 'BaseLayout',
    presets: [window.SwaggerUIBundle.presets.apis],
    validatorUrl: null,
    supportedSubmitMethods: ['get', 'post', 'put', 'delete', 'patch'],
  });
})();
