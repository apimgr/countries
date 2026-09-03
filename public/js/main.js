// Main JavaScript for Countries API frontend
document.addEventListener('DOMContentLoaded', function() {
    // Add smooth scrolling for anchor links
    document.querySelectorAll('a[href^="#"]').forEach(anchor => {
        anchor.addEventListener('click', function (e) {
            e.preventDefault();
            const targetId = this.getAttribute('href').substring(1);
            const targetElement = document.getElementById(targetId);
            if (targetElement) {
                targetElement.scrollIntoView({
                    behavior: 'smooth'
                });
            }
        });
    });

    // Add hover effects to country cards
    document.querySelectorAll('.country-card').forEach(card => {
        card.addEventListener('mouseenter', function() {
            this.style.transform = 'translateY(-8px)';
        });
        
        card.addEventListener('mouseleave', function() {
            this.style.transform = 'translateY(-5px)';
        });
    });

    // Add loading spinner for API calls
    function showLoading() {
        const loader = document.createElement('div');
        loader.id = 'api-loader';
        loader.innerHTML = `
            <div style="
                position: fixed;
                top: 0;
                left: 0;
                width: 100%;
                height: 100%;
                background-color: rgba(40, 42, 54, 0.8);
                display: flex;
                justify-content: center;
                align-items: center;
                z-index: 9999;
            ">
                <div style="
                    color: var(--purple);
                    font-size: 2em;
                    animation: spin 1s linear infinite;
                ">🌍</div>
            </div>
            <style>
                @keyframes spin {
                    from { transform: rotate(0deg); }
                    to { transform: rotate(360deg); }
                }
            </style>
        `;
        document.body.appendChild(loader);
    }

    function hideLoading() {
        const loader = document.getElementById('api-loader');
        if (loader) {
            loader.remove();
        }
    }

    // Add copy functionality for API endpoints
    function addCopyFunctionality() {
        document.querySelectorAll('code').forEach(codeElement => {
            if (!codeElement.classList.contains('copy-enabled')) {
                codeElement.classList.add('copy-enabled');
                codeElement.style.cursor = 'pointer';
                codeElement.title = 'Click to copy';
                
                codeElement.addEventListener('click', function() {
                    const textToCopy = this.textContent;
                    navigator.clipboard.writeText(textToCopy).then(function() {
                        // Visual feedback
                        const originalColor = codeElement.style.color;
                        const originalText = codeElement.textContent;
                        
                        codeElement.style.color = 'var(--green)';
                        codeElement.textContent = '✅ Copied!';
                        
                        setTimeout(function() {
                            codeElement.style.color = originalColor;
                            codeElement.textContent = originalText;
                        }, 1500);
                    }).catch(function() {
                        console.warn('Failed to copy to clipboard');
                    });
                });
            }
        });
    }

    // Initialize copy functionality
    addCopyFunctionality();

    // Add keyboard navigation support
    document.addEventListener('keydown', function(e) {
        // Press 'H' to go home
        if (e.key === 'h' || e.key === 'H') {
            if (!e.ctrlKey && !e.altKey && e.target.tagName !== 'INPUT') {
                window.location.href = '/';
            }
        }
        
        // Press 'D' to go to docs
        if (e.key === 'd' || e.key === 'D') {
            if (!e.ctrlKey && !e.altKey && e.target.tagName !== 'INPUT') {
                window.location.href = '/docs';
            }
        }
        
        // Press 'A' to go to API
        if (e.key === 'a' || e.key === 'A') {
            if (!e.ctrlKey && !e.altKey && e.target.tagName !== 'INPUT') {
                window.location.href = '/api/v1/countries';
            }
        }
    });

    // Add search functionality enhancement
    const searchInput = document.getElementById('searchInput');
    if (searchInput) {
        let searchTimeout;
        
        searchInput.addEventListener('input', function(e) {
            clearTimeout(searchTimeout);
            searchTimeout = setTimeout(() => {
                const searchTerm = e.target.value.toLowerCase().trim();
                
                if (searchTerm.length >= 2) {
                    // Add search highlighting
                    document.querySelectorAll('.country-card').forEach(card => {
                        const name = card.querySelector('.country-name').textContent.toLowerCase();
                        const code = card.querySelector('.country-code').textContent.toLowerCase();
                        const capital = card.querySelector('.country-capital').textContent.toLowerCase();
                        
                        if (name.includes(searchTerm) || code.includes(searchTerm) || capital.includes(searchTerm)) {
                            card.style.display = 'block';
                            card.style.border = '2px solid var(--green)';
                        } else {
                            card.style.display = 'none';
                        }
                    });
                } else if (searchTerm === '') {
                    // Reset all cards
                    document.querySelectorAll('.country-card').forEach(card => {
                        card.style.display = 'block';
                        card.style.border = '2px solid var(--comment)';
                    });
                }
            }, 300);
        });

        // Add search shortcuts
        searchInput.addEventListener('keydown', function(e) {
            if (e.key === 'Escape') {
                this.value = '';
                this.dispatchEvent(new Event('input'));
                this.blur();
            }
        });
    }

    // Add theme toggle functionality (for future enhancement)
    function addThemeToggle() {
        const themeToggle = document.createElement('button');
        themeToggle.innerHTML = '🌙';
        themeToggle.style.cssText = `
            position: fixed;
            top: 20px;
            right: 20px;
            background-color: var(--current-line);
            border: 2px solid var(--purple);
            color: var(--foreground);
            padding: 10px;
            border-radius: 50%;
            cursor: pointer;
            z-index: 1000;
            font-size: 1.2em;
            transition: all 0.3s ease;
        `;
        
        themeToggle.addEventListener('click', function() {
            // This could be extended to toggle between themes
            this.innerHTML = this.innerHTML === '🌙' ? '☀️' : '🌙';
        });
        
        // Uncomment to add theme toggle button
        // document.body.appendChild(themeToggle);
    }

    // Add performance monitoring
    function addPerformanceMonitoring() {
        if ('performance' in window) {
            const loadTime = performance.timing.loadEventEnd - performance.timing.navigationStart;
            console.log(`🚀 Page loaded in ${loadTime}ms`);
            
            // Log API response times for debugging
            const originalFetch = window.fetch;
            window.fetch = function(...args) {
                const start = performance.now();
                return originalFetch(...args).then(response => {
                    const end = performance.now();
                    if (args[0] && args[0].includes('/api/')) {
                        console.log(`📡 API call to ${args[0]} took ${(end - start).toFixed(2)}ms`);
                    }
                    return response;
                });
            };
        }
    }

    // Initialize performance monitoring in development
    if (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1') {
        addPerformanceMonitoring();
    }

    // Add accessibility improvements
    function addAccessibilityFeatures() {
        // Add skip link for keyboard navigation
        const skipLink = document.createElement('a');
        skipLink.href = '#main-content';
        skipLink.textContent = 'Skip to main content';
        skipLink.style.cssText = `
            position: absolute;
            top: -40px;
            left: 6px;
            background: var(--purple);
            color: white;
            padding: 8px;
            text-decoration: none;
            z-index: 1000;
            border-radius: 4px;
        `;
        
        skipLink.addEventListener('focus', function() {
            this.style.top = '6px';
        });
        
        skipLink.addEventListener('blur', function() {
            this.style.top = '-40px';
        });
        
        document.body.insertBefore(skipLink, document.body.firstChild);
        
        // Add main content ID for skip link
        const main = document.querySelector('main');
        if (main && !main.id) {
            main.id = 'main-content';
        }
    }

    // Initialize accessibility features
    addAccessibilityFeatures();

    console.log('🌍 Countries API frontend initialized');
    console.log('💡 Keyboard shortcuts: H (home), D (docs), A (api)');
});