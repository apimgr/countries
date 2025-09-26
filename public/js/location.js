// Location finder JavaScript
document.addEventListener('DOMContentLoaded', function() {
    console.log('🌍 Location page JavaScript loaded');
    
    let currentLocationData = null;

    // DOM elements
    const getLocationBtn = document.getElementById('getLocationBtn');
    const manualLocationBtn = document.getElementById('manualLocationBtn');
    const testLocationBtn = document.getElementById('testLocationBtn');
    const loadingIndicator = document.getElementById('loadingIndicator');
    const errorMessage = document.getElementById('errorMessage');
    const errorText = document.getElementById('errorText');
    const manualInputForm = document.getElementById('manualInputForm');
    const locationResult = document.getElementById('locationResult');
    const findByCoordinatesBtn = document.getElementById('findByCoordinatesBtn');
    const cancelManualBtn = document.getElementById('cancelManualBtn');
    const findAgainBtn = document.getElementById('findAgainBtn');

    // Check if all elements are found
    console.log('🔍 DOM elements found:', {
        getLocationBtn: !!getLocationBtn,
        manualLocationBtn: !!manualLocationBtn,
        testLocationBtn: !!testLocationBtn,
        errorMessage: !!errorMessage,
        errorText: !!errorText
    });

    // Add pulse animation
    const style = document.createElement('style');
    style.textContent = `
        @keyframes pulse {
            0% { opacity: 1; }
            50% { opacity: 0.5; }
            100% { opacity: 1; }
        }
    `;
    document.head.appendChild(style);

    function showError(message) {
        console.log('❌ Showing error:', message);
        if (errorText) {
            errorText.innerHTML = message.replace(/\n/g, '<br>');
        }
        if (errorMessage) {
            errorMessage.style.display = 'block';
        }
        if (loadingIndicator) {
            loadingIndicator.style.display = 'none';
        }
    }

    function hideError() {
        if (errorMessage) {
            errorMessage.style.display = 'none';
        }
    }

    function showLoading() {
        console.log('⏳ Showing loading indicator');
        if (loadingIndicator) {
            loadingIndicator.style.display = 'block';
        }
        hideError();
    }

    function hideLoading() {
        if (loadingIndicator) {
            loadingIndicator.style.display = 'none';
        }
    }

    async function findNearestCountry(longitude, latitude) {
        try {
            console.log(`🔍 Finding nearest country for: ${latitude}, ${longitude}`);
            showLoading();
            
            const response = await fetch('/api/v1/coordinates', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({ longitude, latitude })
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.error || 'Failed to find nearest country');
            }

            const result = await response.json();
            console.log('✅ API result:', result);
            displayResult(result);
            
        } catch (error) {
            console.error('❌ Error finding nearest country:', error);
            showError(error.message);
        } finally {
            hideLoading();
        }
    }

    function displayResult(result) {
        console.log('📊 Displaying result:', result);
        currentLocationData = result;
        
        // Update result display
        const resultCountryName = document.getElementById('resultCountryName');
        const resultCountryCode = document.getElementById('resultCountryCode');
        const yourCoordinates = document.getElementById('yourCoordinates');
        const distanceInfo = document.getElementById('distanceInfo');
        const resultCapital = document.getElementById('resultCapital');
        const countryCoordinates = document.getElementById('countryCoordinates');
        
        if (resultCountryName) resultCountryName.textContent = result.country.name;
        if (resultCountryCode) resultCountryCode.textContent = result.country.country_code;
        if (yourCoordinates) yourCoordinates.textContent = `${result.coordinates.latitude.toFixed(4)}, ${result.coordinates.longitude.toFixed(4)}`;
        if (distanceInfo) distanceInfo.textContent = `${result.distance} km away`;
        if (resultCapital) resultCapital.textContent = result.country.capital || 'N/A';
        if (countryCoordinates) countryCoordinates.textContent = `${result.country.latlng[0]}, ${result.country.latlng[1]}`;

        // Update timezones
        const timezonesList = document.getElementById('resultTimezones');
        if (timezonesList) {
            timezonesList.innerHTML = '';
            result.country.timezones.forEach(timezone => {
                const li = document.createElement('li');
                li.textContent = timezone;
                timezonesList.appendChild(li);
            });
        }

        // Update action buttons
        const viewCountryBtn = document.getElementById('viewCountryBtn');
        const mapLinkBtn = document.getElementById('mapLinkBtn');
        
        if (viewCountryBtn) viewCountryBtn.href = `/country/${result.country.country_code}`;
        if (mapLinkBtn) mapLinkBtn.href = `https://www.google.com/maps?q=${result.coordinates.latitude},${result.coordinates.longitude}`;

        // Show result and hide other sections
        if (locationResult) locationResult.style.display = 'block';
        if (manualInputForm) manualInputForm.style.display = 'none';
    }

    // Event listeners with detailed logging
    if (getLocationBtn) {
        getLocationBtn.addEventListener('click', function() {
            console.log('🎯 Get My Location button clicked');
            
            if (!navigator.geolocation) {
                console.error('❌ Geolocation not supported');
                showError('Geolocation is not supported by this browser');
                return;
            }

            // Check if we're on HTTPS or localhost
            const isSecure = location.protocol === 'https:' || 
                           location.hostname === 'localhost' || 
                           location.hostname === '127.0.0.1';
            if (!isSecure) {
                console.warn('⚠️ Geolocation requires HTTPS in production');
                showError('Geolocation requires HTTPS.<br>Please use HTTPS or localhost for testing.');
                return;
            }

            console.log('📍 Requesting geolocation...');
            showLoading();
            
            navigator.geolocation.getCurrentPosition(
                function(position) {
                    console.log('✅ Position received:', position.coords);
                    const { latitude, longitude } = position.coords;
                    console.log(`📍 Coordinates: ${latitude}, ${longitude}`);
                    findNearestCountry(longitude, latitude);
                },
                function(error) {
                    console.error('❌ Geolocation error:', error);
                    let message = 'Unable to get your location';
                    let debugInfo = '';
                    
                    switch(error.code) {
                        case error.PERMISSION_DENIED:
                            message = 'Location access denied by user';
                            debugInfo = 'Please allow location access and try again.';
                            break;
                        case error.POSITION_UNAVAILABLE:
                            message = 'Location information is unavailable';
                            debugInfo = 'Your device cannot determine your location.';
                            break;
                        case error.TIMEOUT:
                            message = 'Location request timed out';
                            debugInfo = 'Location request took too long. Try again.';
                            break;
                        default:
                            message = `Geolocation error: ${error.message}`;
                            debugInfo = `Error code: ${error.code}`;
                    }
                    
                    console.error(`❌ ${message} - ${debugInfo}`);
                    showError(`${message}<br>${debugInfo}`);
                    hideLoading();
                },
                {
                    enableHighAccuracy: true,
                    timeout: 15000,
                    maximumAge: 300000
                }
            );
        });
        console.log('✅ Get Location button event listener added');
    } else {
        console.error('❌ Get Location button not found in DOM');
    }

    if (testLocationBtn) {
        testLocationBtn.addEventListener('click', function() {
            console.log('🧪 Test location button clicked');
            hideError();
            
            // Test with New York coordinates
            const testLat = 40.7128;
            const testLng = -74.0060;
            
            console.log(`🧪 Testing with coordinates: ${testLat}, ${testLng} (New York)`);
            findNearestCountry(testLng, testLat);
        });
        console.log('✅ Test Location button event listener added');
    } else {
        console.error('❌ Test Location button not found in DOM');
    }

    if (manualLocationBtn) {
        manualLocationBtn.addEventListener('click', function() {
            console.log('📝 Manual location button clicked');
            if (manualInputForm) manualInputForm.style.display = 'block';
            if (locationResult) locationResult.style.display = 'none';
            hideError();
        });
        console.log('✅ Manual Location button event listener added');
    } else {
        console.error('❌ Manual Location button not found in DOM');
    }

    if (cancelManualBtn) {
        cancelManualBtn.addEventListener('click', function() {
            console.log('❌ Cancel manual input');
            if (manualInputForm) manualInputForm.style.display = 'none';
            const latInput = document.getElementById('latInput');
            const lngInput = document.getElementById('lngInput');
            if (latInput) latInput.value = '';
            if (lngInput) lngInput.value = '';
        });
        console.log('✅ Cancel button event listener added');
    }

    if (findByCoordinatesBtn) {
        findByCoordinatesBtn.addEventListener('click', function() {
            console.log('🔍 Find by coordinates clicked');
            const latInput = document.getElementById('latInput');
            const lngInput = document.getElementById('lngInput');
            
            if (!latInput || !lngInput) {
                showError('Input fields not found');
                return;
            }
            
            const lat = parseFloat(latInput.value);
            const lng = parseFloat(lngInput.value);

            if (isNaN(lat) || isNaN(lng)) {
                showError('Please enter valid numeric coordinates');
                return;
            }

            if (lat < -90 || lat > 90) {
                showError('Latitude must be between -90 and 90');
                return;
            }

            if (lng < -180 || lng > 180) {
                showError('Longitude must be between -180 and 180');
                return;
            }

            findNearestCountry(lng, lat);
        });
        console.log('✅ Find by coordinates button event listener added');
    }

    if (findAgainBtn) {
        findAgainBtn.addEventListener('click', function() {
            console.log('🔄 Find again clicked');
            if (locationResult) locationResult.style.display = 'none';
            if (manualInputForm) manualInputForm.style.display = 'none';
            hideError();
            const latInput = document.getElementById('latInput');
            const lngInput = document.getElementById('lngInput');
            if (latInput) latInput.value = '';
            if (lngInput) lngInput.value = '';
        });
        console.log('✅ Find again button event listener added');
    }

    // Allow Enter key in input fields
    const latInput = document.getElementById('latInput');
    const lngInput = document.getElementById('lngInput');
    
    if (latInput) {
        latInput.addEventListener('keypress', function(e) {
            if (e.key === 'Enter' && findByCoordinatesBtn) {
                console.log('⏎ Enter pressed in latitude field');
                findByCoordinatesBtn.click();
            }
        });
    }

    if (lngInput) {
        lngInput.addEventListener('keypress', function(e) {
            if (e.key === 'Enter' && findByCoordinatesBtn) {
                console.log('⏎ Enter pressed in longitude field');
                findByCoordinatesBtn.click();
            }
        });
    }

    console.log('📍 Location finder fully initialized');
});