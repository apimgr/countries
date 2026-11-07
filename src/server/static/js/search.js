        const searchForm = document.getElementById('searchForm');
        const loading = document.getElementById('loading');
        const results = document.getElementById('results');
        const resultsList = document.getElementById('resultsList');
        const resultCount = document.getElementById('resultCount');
        const emptyState = document.getElementById('emptyState');

        searchForm.addEventListener('submit', async (e) => {
            e.preventDefault();

            const query = document.getElementById('query').value.trim();

            // Show loading state
            loading.hidden = false;
            results.hidden = true;
            emptyState.hidden = true;

            try {
                const apiBasePath = document.querySelector('meta[name="api-base-path"]').content;
                const response = await fetch(`${apiBasePath}/countries/search?q=${encodeURIComponent(query)}`);
                const data = await response.json();

                loading.hidden = true;

                if (!data.ok || !data.data || data.data.length === 0) {
                    emptyState.hidden = false;
                    return;
                }

                results.hidden = false;
                resultCount.textContent = `${data.data.length} ${data.data.length === 1 ? 'country' : 'countries'} found`;

                resultsList.innerHTML = data.data.map(country => `
                    <div class="result-card">
                        <div class="result-card-header">
                            <h3 class="result-card-title">${escapeHtml(country.name.common)}</h3>
                            <span class="result-card-badge">${escapeHtml(country.cca2)}</span>
                        </div>
                        <div class="result-card-body">
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">Capital</span>
                                <span class="result-card-stat-value">${country.capital && country.capital.length ? escapeHtml(country.capital.join(', ')) : 'N/A'}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">Latitude</span>
                                <span class="result-card-stat-value">${country.latlng && country.latlng[0] ? country.latlng[0].toFixed(2) : 'N/A'}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">Longitude</span>
                                <span class="result-card-stat-value">${country.latlng && country.latlng[1] ? country.latlng[1].toFixed(2) : 'N/A'}</span>
                            </div>
                        </div>
                        ${country.timezones && country.timezones.length > 0 ? `
                        <div class="result-card-footer">
                            <span class="result-card-stat-label">Timezones</span>
                            <span class="result-card-timezones">${country.timezones.slice(0, 3).join(', ')}${country.timezones.length > 3 ? '...' : ''}</span>
                        </div>
                        ` : ''}
                    </div>
                `).join('');
            } catch (error) {
                loading.hidden = true;
                console.error('Search error:', error);
                emptyState.querySelector('.empty-state-title').textContent = 'Search Error';
                emptyState.querySelector('.empty-state-description').textContent = 'Unable to search countries. Please try again.';
                emptyState.hidden = false;
            }
        });

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        // Focus input on page load
        document.getElementById('query').focus();
