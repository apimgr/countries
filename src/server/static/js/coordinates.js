        const coordForm = document.getElementById('coordForm');
        const useLocationBtn = document.getElementById('useLocation');
        const locationStatus = document.getElementById('locationStatus');
        const loading = document.getElementById('loading');
        const results = document.getElementById('results');
        const resultCard = document.getElementById('resultCard');
        const emptyState = document.getElementById('emptyState');

        // Use My Location button
        useLocationBtn.addEventListener('click', () => {
            if (!navigator.geolocation) {
                locationStatus.querySelector('.location-status-text').textContent = 'Geolocation is not supported by your browser.';
                locationStatus.hidden = false;
                return;
            }

            locationStatus.hidden = false;
            locationStatus.querySelector('.location-status-text').textContent = 'Getting your location...';

            navigator.geolocation.getCurrentPosition(
                (position) => {
                    document.getElementById('lat').value = position.coords.latitude.toFixed(6);
                    document.getElementById('lon').value = position.coords.longitude.toFixed(6);
                    locationStatus.querySelector('.location-status-text').textContent = 'Location found!';
                    setTimeout(() => {
                        locationStatus.hidden = true;
                    }, 2000);
                },
                (error) => {
                    locationStatus.hidden = true;
                    let message = 'Unable to get your location';
                    switch (error.code) {
                        case error.PERMISSION_DENIED:
                            message = 'Location permission denied';
                            break;
                        case error.POSITION_UNAVAILABLE:
                            message = 'Location information unavailable';
                            break;
                        case error.TIMEOUT:
                            message = 'Location request timed out';
                            break;
                    }
                    locationStatus.querySelector('.location-status-text').textContent = message;
                    locationStatus.hidden = false;
                },
                { enableHighAccuracy: true, timeout: 10000, maximumAge: 0 }
            );
        });

        // Form submission
        coordForm.addEventListener('submit', async (e) => {
            e.preventDefault();

            const lat = parseFloat(document.getElementById('lat').value);
            const lon = parseFloat(document.getElementById('lon').value);

            // Validate coordinates
            if (lat < -90 || lat > 90) {
                emptyState.querySelector('.empty-state-title').textContent = 'Invalid latitude';
                emptyState.querySelector('.empty-state-description').textContent = 'Latitude must be between -90 and 90.';
                emptyState.hidden = false;
                return;
            }
            if (lon < -180 || lon > 180) {
                emptyState.querySelector('.empty-state-title').textContent = 'Invalid longitude';
                emptyState.querySelector('.empty-state-description').textContent = 'Longitude must be between -180 and 180.';
                emptyState.hidden = false;
                return;
            }

            // Show loading state
            loading.hidden = false;
            results.hidden = true;
            emptyState.hidden = true;

            try {
                const apiBasePath = document.querySelector('meta[name="api-base-path"]').content;
                const response = await fetch(`${apiBasePath}/coordinates?lat=${lat}&lon=${lon}`);
                const data = await response.json();

                loading.hidden = true;

                if (!data.ok || !data.data) {
                    emptyState.hidden = false;
                    return;
                }

                const country = data.data;
                results.hidden = false;

                resultCard.innerHTML = `
                    <div class="result-card result-card-featured">
                        <div class="result-card-header">
                            <h3 class="result-card-title">${escapeHtml(country.name.common)}</h3>
                            <span class="result-card-badge">${escapeHtml(country.cca2)}</span>
                        </div>
                        <div class="result-card-body">
                            <div class="result-card-stat result-card-stat-highlight">
                                <span class="result-card-stat-label">Distance</span>
                                <span class="result-card-stat-value">${country.distance ? country.distance.toFixed(2) + ' km' : 'N/A'}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">Capital</span>
                                <span class="result-card-stat-value">${country.capital && country.capital.length ? escapeHtml(country.capital.join(', ')) : 'N/A'}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">Coordinates</span>
                                <span class="result-card-stat-value">${country.latlng ? country.latlng.join(', ') : 'N/A'}</span>
                            </div>
                        </div>
                        ${country.timezones && country.timezones.length > 0 ? `
                        <div class="result-card-footer">
                            <span class="result-card-stat-label">Timezones</span>
                            <span class="result-card-timezones">${country.timezones.join(', ')}</span>
                        </div>
                        ` : ''}
                    </div>
                `;
            } catch (error) {
                loading.hidden = true;
                console.error('Search error:', error);
                emptyState.querySelector('.empty-state-title').textContent = 'Search Error';
                emptyState.querySelector('.empty-state-description').textContent = 'Unable to find nearest country. Please try again.';
                emptyState.hidden = false;
            }
        });

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }
