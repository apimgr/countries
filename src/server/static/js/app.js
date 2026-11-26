(() => {
// Countries API - Main JavaScript

const offlineIndicator = document.getElementById('offline-indicator');
const bootRecoveryKey = 'countries_recovered';
window.OFFLINE_STRINGS = offlineIndicator ? {
    lang: document.documentElement.lang || 'en',
    title: offlineIndicator.dataset.offlineTitle || 'Offline',
    heading: offlineIndicator.dataset.offlineTitle || 'Offline',
    message: offlineIndicator.dataset.offlineMessage || 'You are offline.',
    reload: offlineIndicator.dataset.offlineReload || 'Reload'
} : null;
if (window.OFFLINE_STRINGS && 'caches' in window) {
    const appBase = document.querySelector('meta[name="url-prefix"]')?.content || '';
    caches.open(offlineIndicator.dataset.offlineCache || 'Countries API-offline-strings')
        .then(cache => cache.put(new URL(`${appBase}/offline-strings.json`, window.location.origin), new Response(JSON.stringify(window.OFFLINE_STRINGS), { headers: { 'Content-Type': 'application/json' } })))
        .catch(() => {});
}
function clearBootRecoveryTimer() {
    window.requestAnimationFrame(() => window.clearTimeout(bootRecoveryTimer));
}
function showBootRecoveryError(copy) {
    const body = document.body;
    const main = document.createElement('main');
    const heading = document.createElement('h1');
    const message = document.createElement('p');
    const reload = document.createElement('a');
    main.className = 'page-content error-page';
    heading.textContent = copy.heading || copy.title || 'Offline';
    message.textContent = copy.message || 'You are offline.';
    reload.href = window.location.href;
    reload.textContent = copy.reload || 'Reload';
    main.append(heading, message, reload);
    body.replaceChildren(main);
}
async function readOfflineStrings() {
    const fallback = window.OFFLINE_STRINGS || { lang: 'en', title: 'Offline', heading: 'You are offline', message: 'You are offline.', reload: 'Reload' };
    if (!offlineIndicator || !('caches' in window)) return fallback;
    try {
        const appBase = document.querySelector('meta[name="url-prefix"]')?.content || '';
        const cache = await caches.open(offlineIndicator.dataset.offlineCache || 'Countries API-offline-strings');
        const stored = await cache.match(new URL(`${appBase}/offline-strings.json`, window.location.origin));
        return stored ? Object.assign({}, fallback, await stored.json()) : fallback;
    } catch {
        return fallback;
    }
}
const bootRecoveryTimer = window.setTimeout(async () => {
    let recovered;
    try {
        recovered = window.sessionStorage.getItem(bootRecoveryKey) === '1';
    } catch {
        showBootRecoveryError(await readOfflineStrings());
        return;
    }
    if (recovered) {
        showBootRecoveryError(await readOfflineStrings());
        return;
    }
    try {
        window.sessionStorage.setItem(bootRecoveryKey, '1');
        const registrations = 'serviceWorker' in navigator ? await navigator.serviceWorker.getRegistrations() : [];
        await Promise.all(registrations.map(registration => registration.unregister()));
        if ('caches' in window) await Promise.all((await caches.keys()).map(name => caches.delete(name)));
        window.location.reload();
    } catch {
        showBootRecoveryError(await readOfflineStrings());
    }
}, 10000);
if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', clearBootRecoveryTimer, { once: true });
} else {
    clearBootRecoveryTimer();
}
function updateOfflineIndicator() {
    if (!offlineIndicator) return;
    offlineIndicator.hidden = navigator.onLine;
}
window.addEventListener('online', updateOfflineIndicator);
window.addEventListener('offline', updateOfflineIndicator);
updateOfflineIndicator();

// Register service worker for PWA support
if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => {
        const prefix = document.querySelector('meta[name="url-prefix"]')?.content || '';
        navigator.serviceWorker.register(`${prefix}/sw.js`)
            .then(registration => {
                console.log('ServiceWorker registered:', registration.scope);
                const checkWaiting = () => {
                    if (registration.waiting && navigator.serviceWorker.controller) showUpdateNotification(registration.waiting);
                };
                registration.addEventListener('updatefound', () => {
                    const worker = registration.installing;
                    if (!worker) return;
                    worker.addEventListener('statechange', () => {
                        if (worker.state === 'installed') checkWaiting();
                    });
                });
                checkWaiting();
                window.setInterval(() => registration.update().catch(() => {}), 60 * 60 * 1000);
            })
            .catch(error => {
                console.log('ServiceWorker registration failed:', error);
            });
    });

    let updateAccepted = false;
    let reloading = false;
    function reloadOnce() {
        if (reloading) return;
        reloading = true;
        window.location.reload();
    }
    navigator.serviceWorker.addEventListener('controllerchange', () => {
        if (updateAccepted) reloadOnce();
    });

    let deferredInstallPrompt = null;
    const installButton = document.createElement('button');
    installButton.type = 'button';
    installButton.className = 'pwa-install-button';
    installButton.hidden = true;
    installButton.textContent = document.getElementById('toast-container')?.dataset.installLabel || '';
    const footer = document.querySelector('.footer .footer-content');
    const installed = window.matchMedia('(display-mode: standalone)').matches || window.navigator.standalone === true;
    if (footer && !installed) footer.appendChild(installButton);

    function showIOSInstallInstructions() {
        const copy = document.getElementById('toast-container')?.dataset;
        const dialog = document.createElement('dialog');
        dialog.className = 'pwa-install-dialog';
        const title = document.createElement('h2');
        title.id = 'pwa-install-title';
        title.textContent = copy?.iosInstallTitle || '';
        const steps = document.createElement('ol');
        for (const key of ['iosInstallStep1', 'iosInstallStep2', 'iosInstallStep3']) {
            const step = document.createElement('li');
            step.textContent = copy?.[key] || '';
            steps.appendChild(step);
        }
        const close = document.createElement('button');
        close.type = 'button';
        close.textContent = copy?.iosInstallClose || '';
        close.addEventListener('click', () => dialog.close());
        dialog.setAttribute('aria-labelledby', title.id);
        dialog.append(title, steps, close);
        document.body.appendChild(dialog);
        dialog.addEventListener('close', () => dialog.remove(), { once: true });
        dialog.showModal();
    }

    if (!installed && /iPad|iPhone|iPod/.test(navigator.userAgent)) {
        installButton.hidden = false;
        installButton.addEventListener('click', showIOSInstallInstructions);
    }

    window.addEventListener('beforeinstallprompt', event => {
        event.preventDefault();
        deferredInstallPrompt = event;
        if (!installed) installButton.hidden = false;
    });
    installButton.addEventListener('click', async () => {
        if (!deferredInstallPrompt) return;
        await deferredInstallPrompt.prompt();
        await deferredInstallPrompt.userChoice;
        deferredInstallPrompt = null;
        installButton.hidden = true;
    });
    window.addEventListener('appinstalled', () => {
        deferredInstallPrompt = null;
        installButton.hidden = true;
    });

    function showUpdateNotification(worker) {
        if (document.querySelector('.update-banner')) return;
        const copy = document.getElementById('toast-container');
        const banner = document.createElement('section');
        banner.className = 'update-banner';
        banner.setAttribute('role', 'status');
        banner.setAttribute('aria-live', 'polite');
        const label = document.createElement('span');
        label.textContent = copy?.dataset.updateAvailable || '';
        const updateButton = document.createElement('button');
        updateButton.type = 'button';
        updateButton.textContent = copy?.dataset.updateNow || '';
        updateButton.addEventListener('click', () => {
            updateAccepted = true;
            worker.postMessage({ type: 'SKIP_WAITING' });
            window.setTimeout(reloadOnce, 5000);
        });
        const laterButton = document.createElement('button');
        laterButton.type = 'button';
        laterButton.textContent = copy?.dataset.updateLater || '';
        laterButton.addEventListener('click', () => banner.remove());
        banner.append(label, updateButton, laterButton);
        document.body.appendChild(banner);
    }
}

// Helper function to format numbers
function formatNumber(num) {
    return new Intl.NumberFormat().format(num);
}

// API helper
async function fetchAPI(endpoint) {
    const response = await fetch(endpoint);
    if (!response.ok) {
        throw new Error(`API error: ${response.status}`);
    }
    return response.json();
}
})();

(() => {
const searchForm = document.getElementById('searchForm');
        const loading = document.getElementById('loading');
        const results = document.getElementById('results');
        const resultsList = document.getElementById('resultsList');
        const resultCount = document.getElementById('resultCount');
        const emptyState = document.getElementById('emptyState');
        if (!searchForm) return;

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
                const countTemplate = data.data.length === 1 ? searchForm.dataset.resultCountOne : searchForm.dataset.resultCountMany;
                resultCount.textContent = countTemplate.replace('{count}', String(data.data.length));

                resultsList.innerHTML = data.data.map(country => `
                    <div class="result-card">
                        <div class="result-card-header">
                            <h3 class="result-card-title">${escapeHtml(country.name.common)}</h3>
                            <span class="result-card-badge">${escapeHtml(country.cca2)}</span>
                        </div>
                        <div class="result-card-body">
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">${escapeHtml(searchForm.dataset.capitalLabel)}</span>
                                <span class="result-card-stat-value">${country.capital && country.capital.length ? escapeHtml(country.capital.join(', ')) : escapeHtml(searchForm.dataset.notAvailable)}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">${escapeHtml(searchForm.dataset.latitudeLabel)}</span>
                                <span class="result-card-stat-value">${country.latlng && country.latlng[0] ? country.latlng[0].toFixed(2) : escapeHtml(searchForm.dataset.notAvailable)}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">${escapeHtml(searchForm.dataset.longitudeLabel)}</span>
                                <span class="result-card-stat-value">${country.latlng && country.latlng[1] ? country.latlng[1].toFixed(2) : escapeHtml(searchForm.dataset.notAvailable)}</span>
                            </div>
                        </div>
                        ${country.timezones && country.timezones.length > 0 ? `
                        <div class="result-card-footer">
                            <span class="result-card-stat-label">${escapeHtml(searchForm.dataset.timezonesLabel)}</span>
                            <span class="result-card-timezones">${country.timezones.slice(0, 3).join(', ')}${country.timezones.length > 3 ? '...' : ''}</span>
                        </div>
                        ` : ''}
                    </div>
                `).join('');
            } catch (error) {
                loading.hidden = true;
                console.error('Search error:', error);
                emptyState.querySelector('.empty-state-title').textContent = searchForm.dataset.errorTitle;
                emptyState.querySelector('.empty-state-description').textContent = searchForm.dataset.errorDescription;
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
})();

(() => {
const coordForm = document.getElementById('coordForm');
        const useLocationBtn = document.getElementById('useLocation');
        const locationStatus = document.getElementById('locationStatus');
        const loading = document.getElementById('loading');
        const results = document.getElementById('results');
        const resultCard = document.getElementById('resultCard');
        const emptyState = document.getElementById('emptyState');
        if (!coordForm || !useLocationBtn) return;

        // Use My Location button
        useLocationBtn.addEventListener('click', () => {
            if (!navigator.geolocation) {
                locationStatus.querySelector('.location-status-text').textContent = coordForm.dataset.geolocationUnsupported;
                locationStatus.hidden = false;
                return;
            }

            locationStatus.hidden = false;
            locationStatus.querySelector('.location-status-text').textContent = coordForm.dataset.gettingLocation;

            navigator.geolocation.getCurrentPosition(
                (position) => {
                    document.getElementById('lat').value = position.coords.latitude.toFixed(6);
                    document.getElementById('lon').value = position.coords.longitude.toFixed(6);
                    locationStatus.querySelector('.location-status-text').textContent = coordForm.dataset.locationFound;
                    setTimeout(() => {
                        locationStatus.hidden = true;
                    }, 2000);
                },
                (error) => {
                    locationStatus.hidden = true;
                    let message = coordForm.dataset.unableLocation;
                    switch (error.code) {
                        case error.PERMISSION_DENIED:
                            message = coordForm.dataset.locationPermissionDenied;
                            break;
                        case error.POSITION_UNAVAILABLE:
                            message = coordForm.dataset.locationUnavailable;
                            break;
                        case error.TIMEOUT:
                            message = coordForm.dataset.locationTimeout;
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
                emptyState.querySelector('.empty-state-title').textContent = coordForm.dataset.invalidLatitude;
                emptyState.querySelector('.empty-state-description').textContent = coordForm.dataset.invalidLatitudeDescription;
                emptyState.hidden = false;
                return;
            }
            if (lon < -180 || lon > 180) {
                emptyState.querySelector('.empty-state-title').textContent = coordForm.dataset.invalidLongitude;
                emptyState.querySelector('.empty-state-description').textContent = coordForm.dataset.invalidLongitudeDescription;
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
                                <span class="result-card-stat-label">${escapeHtml(coordForm.dataset.distanceLabel)}</span>
                                <span class="result-card-stat-value">${country.distance ? country.distance.toFixed(2) + ' km' : escapeHtml(coordForm.dataset.notAvailable)}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">${escapeHtml(coordForm.dataset.capitalLabel)}</span>
                                <span class="result-card-stat-value">${country.capital && country.capital.length ? escapeHtml(country.capital.join(', ')) : escapeHtml(searchForm.dataset.notAvailable)}</span>
                            </div>
                            <div class="result-card-stat">
                                <span class="result-card-stat-label">${escapeHtml(coordForm.dataset.coordinatesLabel)}</span>
                                <span class="result-card-stat-value">${country.latlng ? country.latlng.join(', ') : escapeHtml(coordForm.dataset.notAvailable)}</span>
                            </div>
                        </div>
                        ${country.timezones && country.timezones.length > 0 ? `
                        <div class="result-card-footer">
                            <span class="result-card-stat-label">${escapeHtml(coordForm.dataset.timezonesLabel)}</span>
                            <span class="result-card-timezones">${country.timezones.join(', ')}</span>
                        </div>
                        ` : ''}
                    </div>
                `;
            } catch (error) {
                loading.hidden = true;
                console.error('Search error:', error);
                emptyState.querySelector('.empty-state-title').textContent = coordForm.dataset.errorTitle;
                emptyState.querySelector('.empty-state-description').textContent = coordForm.dataset.errorDescription;
                emptyState.hidden = false;
            }
        });

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }
})();

(() => {
"use strict";

const form = document.querySelector("#graphql-form");
const queryInput = document.querySelector("#graphql-query");
const resultOutput = document.querySelector("#graphql-result");
if (!form || !queryInput || !resultOutput) return;

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  resultOutput.textContent = resultOutput.dataset.loading;
  try {
    const apiBasePath = document.querySelector('meta[name="api-base-path"]').content;
    const response = await fetch(`${apiBasePath}/server/graphql`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ query: queryInput.value })
    });
    const payload = await response.json();
    resultOutput.textContent = JSON.stringify(payload, null, 2);
  } catch {
    resultOutput.textContent = resultOutput.dataset.requestFailed;
  }
});
})();

(() => {
  const toggle = document.getElementById('nav-toggle');
  const panel = document.querySelector('.nav-panel');
  const toggleLabel = document.querySelector('.nav-toggle');
  if (!toggle || !panel || !toggleLabel) return;

  const focusable = () => Array.from(panel.querySelectorAll('a[href], [tabindex="0"]'));
  toggle.addEventListener('change', () => {
    if (toggle.checked) {
      focusable()[0]?.focus();
    } else {
      toggleLabel.focus();
    }
  });
  panel.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      toggle.checked = false;
      toggleLabel.focus();
      return;
    }
    if (event.key !== 'Tab') return;
    const items = focusable();
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  });
  panel.querySelector('.nav-close')?.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggle.checked = false;
      toggleLabel.focus();
    }
  });
  const updateNavigation = () => {
    const links = document.querySelector('.header .nav-links');
    const collapsed = window.innerWidth >= 768 && links && links.scrollWidth > links.clientWidth;
    document.documentElement.classList.toggle('nav-collapsed', Boolean(collapsed));
  };
  updateNavigation();
  window.addEventListener('resize', updateNavigation);
})();

(() => {
  const storageKey = document.querySelector('meta[name="owner-token-storage-key"]')?.content;
  const primarySecret = document.querySelector('[data-token-secret][data-token-kind="primary"]');

  if (storageKey && primarySecret) {
    try {
      localStorage.setItem(storageKey, primarySecret.textContent.trim());
    } catch {
      // The HttpOnly owner cookie remains the authoritative browser credential.
    }
  }

  document.addEventListener('click', async (event) => {
    const button = event.target.closest('.copy-btn');
    if (!button) return;
    const value = button.dataset.copy || button.closest('.token-card, .linked-token-card, .code-block')?.querySelector('[data-token-secret], .copy-value')?.textContent.trim() || button.previousElementSibling?.textContent.trim();
    const feedback = button.querySelector('[aria-live="polite"]') || button;
    const originalFeedback = feedback.textContent;
    if (!value || !navigator.clipboard) {
      if (button.dataset.copyFailed) feedback.textContent = button.dataset.copyFailed;
      window.showToast?.(document.getElementById('toast-container')?.dataset.copyFailed || '', 'error');
      return;
    }
    const icon = button.querySelector('.copy-icon');
    const originalIcon = icon?.textContent || '';
    try {
      await navigator.clipboard.writeText(value);
      feedback.textContent = button.dataset.copied || button.dataset.copiedLabel || '';
      if (icon) icon.textContent = '✓';
      button.classList.add('copied');
      window.showToast?.(document.getElementById('toast-container')?.dataset.copySuccess || '', 'success');
      window.setTimeout(() => {
        feedback.textContent = button.dataset.copyLabel || originalFeedback;
        if (icon) icon.textContent = originalIcon;
        button.classList.remove('copied');
      }, 1800);
    } catch {
      if (button.dataset.copyFailed) feedback.textContent = button.dataset.copyFailed;
      window.showToast?.(document.getElementById('toast-container')?.dataset.copyFailed || '', 'error');
    }
  });
})();

(() => {
  const container = document.getElementById('toast-container');
  const queue = [];
  const visible = [];
  let nextID = 1;
  const icons = { success: '✓', info: 'ℹ', warning: '⚠', error: '✗' };

  function durationFor(type, requested) {
    if (type === 'error') return 0;
    if (Number.isFinite(requested)) return Math.max(0, requested);
    if (!container) return type === 'warning' ? 5000 : 3000;
    const seconds = Number(type === 'warning' ? container.dataset.warningDuration : container.dataset.duration);
    return Number.isFinite(seconds) ? Math.max(0, seconds) * 1000 : (type === 'warning' ? 5000 : 3000);
  }

  function finish(toast) {
    if (!toast || toast.dataset.closing === 'true') return;
    toast.dataset.closing = 'true';
    window.clearTimeout(toast.toastTimer);
    toast.classList.remove('toast-visible');
    window.setTimeout(() => {
      toast.remove();
      const index = visible.indexOf(toast);
      if (index >= 0) visible.splice(index, 1);
      while (queue.length && visible.length < 5) display(queue.shift());
    }, 220);
  }

  function display(item) {
    if (!container || visible.length >= 5) {
      queue.push(item);
      return;
    }
    const toast = document.createElement('div');
    toast.className = `toast toast-${item.type}`;
    toast.dataset.toastId = item.id;
    toast.setAttribute('role', item.type === 'error' || item.type === 'warning' ? 'alert' : 'status');
    toast.setAttribute('aria-live', item.type === 'error' ? 'assertive' : 'polite');
    toast.setAttribute('aria-atomic', 'true');

    const icon = document.createElement('span');
    icon.className = 'toast-icon';
    icon.setAttribute('aria-hidden', 'true');
    icon.textContent = icons[item.type];
    const text = document.createElement('span');
    text.className = 'toast-message';
    text.textContent = item.message;
    const close = document.createElement('button');
    close.type = 'button';
    close.className = 'toast-close';
    close.setAttribute('aria-label', container?.dataset.dismissLabel || '');
    close.textContent = '×';
    const progress = document.createElement('div');
    progress.className = 'toast-progress';

    toast.append(icon, text, close, progress);
    container.prepend(toast);
    visible.unshift(toast);
    requestAnimationFrame(() => toast.classList.add('toast-visible'));

    close.addEventListener('click', (event) => {
      event.stopPropagation();
      finish(toast);
    });
    toast.addEventListener('click', () => finish(toast));
    const duration = item.duration;
    if (duration > 0) {
      let remaining = duration;
      let started = Date.now();
      const schedule = () => {
        started = Date.now();
        toast.toastTimer = window.setTimeout(() => finish(toast), remaining);
        progress.style.setProperty('--toast-duration', `${remaining}ms`);
      };
      const pause = () => {
        if (!toast.toastTimer) return;
        window.clearTimeout(toast.toastTimer);
        toast.toastTimer = null;
        remaining = Math.max(0, remaining - (Date.now() - started));
        progress.style.animationPlayState = 'paused';
      };
      toast.addEventListener('mouseenter', pause);
      toast.addEventListener('mouseleave', () => {
        if (!toast.toastTimer && remaining > 0) {
          progress.style.animationPlayState = 'running';
          schedule();
        }
      });
      schedule();
    } else {
      progress.hidden = true;
    }
  }

  window.showToast = function (message, type = 'info', duration) {
    const safeType = Object.prototype.hasOwnProperty.call(icons, type) ? type : 'info';
    const item = { id: String(nextID++), message: String(message), type: safeType, duration: durationFor(safeType, duration) };
    if (!container) return item.id;
    display(item);
    return item.id;
  };

  window.dismissToast = function (id) {
    const toast = visible.find((item) => item.dataset.toastId === String(id));
    if (toast) finish(toast);
    const queued = queue.findIndex((item) => item.id === String(id));
    if (queued >= 0) queue.splice(queued, 1);
  };

  window.dismissAllToasts = function () {
    visible.slice().forEach(finish);
    queue.length = 0;
  };

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && visible.length) finish(visible[0]);
  });

  document.querySelectorAll('.site-banner .site-banner-dismiss').forEach((form) => {
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      if (button) button.disabled = true;
      try {
        const response = await fetch(form.action, { method: 'POST', body: new FormData(form), credentials: 'same-origin' });
        if (!response.ok) throw new Error('Announcement dismissal failed');
        form.closest('.site-banner')?.remove();
      } catch {
        form.submit();
      }
    });
  });
})();
