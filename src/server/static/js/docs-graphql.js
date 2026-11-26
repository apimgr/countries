(() => {
  const mount = document.getElementById('graphiql');
  const graphiql = window.GraphiQL;
  if (!mount || !window.React || !window.ReactDOM || !graphiql || typeof graphiql.createFetcher !== 'function') return;

  const setTheme = () => {
    document.body.classList.remove('graphiql-dark', 'graphiql-light');
    const theme = document.documentElement.className;
    if (theme.includes('theme-dark')) document.body.classList.add('graphiql-dark');
    if (theme.includes('theme-light')) document.body.classList.add('graphiql-light');
  };

  setTheme();
  new MutationObserver(setTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });

  const fetcher = graphiql.createFetcher({ url: mount.dataset.endpoint });
  window.ReactDOM.render(
    window.React.createElement(graphiql, {
      fetcher,
      defaultQuery: mount.dataset.defaultQuery,
      defaultEditorToolsVisibility: true,
    }),
    mount,
  );
})();
