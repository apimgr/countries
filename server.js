const express = require('express');
const helmet = require('helmet');
const rateLimit = require('express-rate-limit');
const cors = require('cors');
const path = require('path');
const swaggerUi = require('swagger-ui-express');
const swaggerJsdoc = require('swagger-jsdoc');

const app = express();
const PORT = process.env.PORT || 3001;

// Load countries data
const countries = require('./countries.json');

// Utility function to calculate distance between two coordinates using Haversine formula
function calculateDistance(lat1, lon1, lat2, lon2) {
  const R = 6371; // Radius of the Earth in kilometers
  const dLat = (lat2 - lat1) * Math.PI / 180;
  const dLon = (lon2 - lon1) * Math.PI / 180;
  const a = 
    Math.sin(dLat/2) * Math.sin(dLat/2) +
    Math.cos(lat1 * Math.PI / 180) * Math.cos(lat2 * Math.PI / 180) * 
    Math.sin(dLon/2) * Math.sin(dLon/2);
  const c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1-a));
  const d = R * c; // Distance in kilometers
  return d;
}

// Trust proxy for reverse proxy support
app.set('trust proxy', true);

// Security middleware
app.use(helmet());

// Rate limiting - 1000 requests per hour
const limiter = rateLimit({
  windowMs: 60 * 60 * 1000, // 1 hour
  max: 1000, // 1000 requests per hour
  message: 'Too many requests, please try again later.',
  standardHeaders: true,
  legacyHeaders: false,
  trustProxy: true, // Explicitly set to work with reverse proxies
});
app.use(limiter);

// CORS - allow all origins
app.use(cors());

// Body parsing middleware
app.use(express.json());
app.use(express.urlencoded({ extended: true }));

// Static files
app.use(express.static(path.join(__dirname, 'public')));

// EJS template engine
app.set('view engine', 'ejs');
app.set('views', path.join(__dirname, 'views'));

// Swagger configuration
const swaggerJsdocOptions = {
  definition: {
    openapi: '3.0.0',
    info: {
      title: 'Countries API',
      version: '1.0.0',
      description: 'A REST API for country information including timezones, coordinates, and capitals',
    },
    servers: [
      {
        url: '/api/v1',
        description: 'API v1',
      },
    ],
  },
  apis: ['./routes/*.js', './server.js'],
};

const swaggerSpec = swaggerJsdoc(swaggerJsdocOptions);

// Swagger UI with custom CSS
const swaggerUIOptions = {
  customCss: `
    body { 
      background-color: #282a36 !important; 
      margin: 0; 
      padding: 0; 
    }
    html { 
      background-color: #282a36 !important; 
    }
    .swagger-ui .topbar { display: none !important; }
    .swagger-ui { 
      font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace !important; 
      background-color: #282a36 !important;
      color: #f8f8f2 !important;
    }
    .swagger-ui .wrapper { 
      background-color: #282a36 !important;
      padding: 20px !important;
    }
    .swagger-ui .scheme-container { 
      background-color: #44475a !important; 
      border: 2px solid #bd93f9 !important;
      border-radius: 10px !important;
      padding: 20px !important;
    }
    .swagger-ui .scheme-container .schemes { 
      background-color: #44475a !important; 
    }
    .swagger-ui .scheme-container .schemes > label { 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .scheme-container select { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important; 
      border: 2px solid #6272a4 !important;
    }
    .swagger-ui .info { 
      background-color: #44475a !important; 
      border: 2px solid #8be9fd !important;
      border-radius: 10px !important;
      padding: 20px !important;
      margin-bottom: 20px !important;
    }
    .swagger-ui .info .title { 
      color: #bd93f9 !important; 
      font-size: 2.5em !important;
      font-weight: bold !important;
    }
    .swagger-ui .info .description { 
      color: #f8f8f2 !important; 
      font-size: 1.1em !important;
      line-height: 1.6 !important;
    }
    .swagger-ui .opblock { 
      background-color: #44475a !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 8px !important;
      margin-bottom: 15px !important;
    }
    .swagger-ui .opblock .opblock-summary { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important;
      border-bottom: 1px solid #6272a4 !important;
    }
    .swagger-ui .opblock .opblock-section-header { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important;
    }
    .swagger-ui .opblock.opblock-get { border-color: #50fa7b !important; }
    .swagger-ui .opblock.opblock-get .opblock-summary { border-color: #50fa7b !important; }
    .swagger-ui .opblock.opblock-get .tab-header .tab-item.active h4 span:after { background: #50fa7b !important; }
    .swagger-ui .opblock.opblock-post { border-color: #ffb86c !important; }
    .swagger-ui .opblock.opblock-post .opblock-summary { border-color: #ffb86c !important; }
    .swagger-ui .opblock.opblock-post .tab-header .tab-item.active h4 span:after { background: #ffb86c !important; }
    .swagger-ui .opblock-tag { 
      color: #8be9fd !important; 
      font-size: 1.5em !important;
      font-weight: bold !important;
      border-bottom: 2px solid #8be9fd !important;
      padding-bottom: 10px !important;
      margin-bottom: 20px !important;
    }
    .swagger-ui .opblock-summary-method { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important;
      font-weight: bold !important;
      border-radius: 5px !important;
    }
    .swagger-ui .opblock-summary-path { 
      color: #ffb86c !important; 
      font-family: monospace !important;
      font-weight: bold !important;
    }
    .swagger-ui .opblock-description-wrapper { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important;
      border-radius: 5px !important;
      padding: 15px !important;
    }
    .swagger-ui .opblock-body { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important;
    }
    .swagger-ui .btn { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important;
      border: none !important;
      border-radius: 5px !important;
      font-weight: bold !important;
      transition: all 0.3s ease !important;
    }
    .swagger-ui .btn:hover { 
      background-color: #ff79c6 !important; 
      transform: translateY(-2px) !important;
    }
    .swagger-ui .parameters-col_description { 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .parameter__name { 
      color: #8be9fd !important; 
      font-weight: bold !important;
    }
    .swagger-ui .parameter__type { 
      color: #50fa7b !important; 
    }
    .swagger-ui .response-col_status { 
      color: #50fa7b !important; 
      font-weight: bold !important;
    }
    .swagger-ui .response-col_description { 
      color: #f8f8f2 !important; 
    }
    .swagger-ui textarea { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 5px !important;
    }
    .swagger-ui input[type="text"], .swagger-ui input[type="password"], .swagger-ui input[type="search"] { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 5px !important;
    }
    .swagger-ui select { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 5px !important;
    }
    .swagger-ui .models { 
      background-color: #44475a !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 10px !important;
      padding: 20px !important;
    }
    .swagger-ui .model-title { 
      color: #bd93f9 !important; 
      font-weight: bold !important;
    }
    .swagger-ui .property-type { 
      color: #50fa7b !important; 
    }
    .swagger-ui .property-name { 
      color: #8be9fd !important; 
    }
    .swagger-ui .renderedMarkdown p { 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .highlight-code { 
      background-color: #282a36 !important; 
    }
    .swagger-ui .microlight { 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .servers { 
      background-color: #44475a !important; 
      border: 2px solid #bd93f9 !important;
      border-radius: 10px !important;
      padding: 15px !important;
      margin-bottom: 20px !important;
    }
    .swagger-ui .servers > label { 
      color: #f8f8f2 !important; 
      font-weight: bold !important;
    }
    .swagger-ui .servers select { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 5px !important;
    }
    .swagger-ui .servers-title { 
      color: #bd93f9 !important; 
      font-weight: bold !important;
    }
    .swagger-ui .download-url-wrapper { 
      background-color: #44475a !important; 
      border: 2px solid #8be9fd !important;
      border-radius: 8px !important;
      padding: 15px !important;
    }
    .swagger-ui .download-url-wrapper .download-url-button { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important; 
    }
    .swagger-ui table { 
      background-color: #44475a !important; 
    }
    .swagger-ui table thead tr th, .swagger-ui table thead tr td { 
      background-color: #6272a4 !important; 
      color: #f8f8f2 !important; 
    }
    .swagger-ui table tbody tr td { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .tab li { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .tab li.active { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important; 
    }
    .swagger-ui .response-control-media-type { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important; 
    }
    .swagger-ui .response-control-media-type--accept-controller select { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important; 
      border: 2px solid #6272a4 !important;
    }
    .swagger-ui .model-container { 
      background-color: #44475a !important; 
      border: 2px solid #6272a4 !important;
      border-radius: 10px !important;
      padding: 20px !important;
    }
    .swagger-ui .model { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important;
    }
    .swagger-ui .model-toggle { 
      background-color: #282a36 !important; 
      color: #bd93f9 !important;
      border: 2px solid #bd93f9 !important;
      border-radius: 5px !important;
    }
    .swagger-ui .model-toggle:hover { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important;
    }
    .swagger-ui .model .property { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important;
      border-bottom: 1px solid #6272a4 !important;
    }
    .swagger-ui .property-row { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important;
    }
    .swagger-ui .property-row .property-name { 
      color: #8be9fd !important; 
    }
    .swagger-ui .property-row .property-type { 
      color: #50fa7b !important; 
    }
    .swagger-ui .property-row .property-format { 
      color: #ffb86c !important; 
    }
    .swagger-ui .model-example { 
      background-color: #282a36 !important; 
      color: #f8f8f2 !important;
      border: 2px solid #6272a4 !important;
      border-radius: 5px !important;
    }
    .swagger-ui .model-example .copy-to-clipboard { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important;
    }
    .swagger-ui .model .deprecated span, .swagger-ui .model .deprecated td { 
      color: #ff5555 !important; 
    }
    .swagger-ui section.models { 
      background-color: #282a36 !important; 
      border: none !important;
    }
    .swagger-ui section.models.is-open { 
      background-color: #282a36 !important; 
    }
    .swagger-ui section.models h4 { 
      color: #bd93f9 !important; 
      background-color: #44475a !important;
      border: 2px solid #bd93f9 !important;
      border-radius: 10px 10px 0 0 !important;
      padding: 15px 20px !important;
      margin: 0 !important;
    }
    .swagger-ui section.models .model-container { 
      background-color: #44475a !important; 
      border: 2px solid #bd93f9 !important;
      border-top: none !important;
      border-radius: 0 0 10px 10px !important;
      padding: 20px !important;
    }
    .swagger-ui .models-jump-to-path { 
      background-color: #44475a !important; 
      color: #f8f8f2 !important;
      border: 2px solid #6272a4 !important;
    }
    .swagger-ui .model-jump-to-path { 
      background-color: #bd93f9 !important; 
      color: #282a36 !important;
    }
    .swagger-ui .loading-container { 
      background-color: #282a36 !important; 
    }
    .swagger-ui .loading { 
      color: #bd93f9 !important; 
    }
    /* Additional catch-all rules for any remaining white areas */
    .swagger-ui * { 
      scrollbar-color: #bd93f9 #44475a !important;
    }
    .swagger-ui ::-webkit-scrollbar { 
      background-color: #44475a !important; 
    }
    .swagger-ui ::-webkit-scrollbar-thumb { 
      background-color: #bd93f9 !important; 
      border-radius: 5px !important;
    }
    .swagger-ui ::-webkit-scrollbar-track { 
      background-color: #282a36 !important; 
    }
  `,
  customSiteTitle: "🌍 Countries API Documentation",
  customfavIcon: "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>🌍</text></svg>",
  swaggerOptions: {
    persistAuthorization: true,
    displayRequestDuration: true,
    filter: true,
    showExtensions: true,
    showCommonExtensions: true
  }
};

app.use('/docs', swaggerUi.serve, swaggerUi.setup(swaggerSpec, swaggerUIOptions));

// Swagger JSON
app.get('/api/docs', (req, res) => {
  res.setHeader('Content-Type', 'application/json');
  res.send(swaggerSpec);
});

// API v1 routes
const apiV1Router = express.Router();

/**
 * @swagger
 * components:
 *   schemas:
 *     Country:
 *       type: object
 *       properties:
 *         name:
 *           type: string
 *           description: Country name
 *         country_code:
 *           type: string
 *           description: Two-letter ISO country code
 *         capital:
 *           type: string
 *           nullable: true
 *           description: Capital city name
 *         latlng:
 *           type: array
 *           items:
 *             type: number
 *           description: Latitude and longitude coordinates
 *         timezones:
 *           type: array
 *           items:
 *             type: string
 *           description: List of timezones
 */

/**
 * @swagger
 * /countries:
 *   get:
 *     summary: Get all countries
 *     tags: [Countries]
 *     responses:
 *       200:
 *         description: List of all countries
 *         content:
 *           application/json:
 *             schema:
 *               type: array
 *               items:
 *                 $ref: '#/components/schemas/Country'
 */
apiV1Router.get('/countries', (req, res) => {
  res.json(countries);
});

/**
 * @swagger
 * /countries/{code}:
 *   get:
 *     summary: Get country by code
 *     tags: [Countries]
 *     parameters:
 *       - in: path
 *         name: code
 *         required: true
 *         schema:
 *           type: string
 *         description: Two-letter country code
 *     responses:
 *       200:
 *         description: Country information
 *         content:
 *           application/json:
 *             schema:
 *               $ref: '#/components/schemas/Country'
 *       404:
 *         description: Country not found
 */
apiV1Router.get('/countries/:code', (req, res) => {
  const code = req.params.code.toUpperCase();
  const country = countries.find(c => c.country_code === code);
  
  if (!country) {
    return res.status(404).json({ error: 'Country not found' });
  }
  
  res.json(country);
});

/**
 * @swagger
 * /countries/search/{name}:
 *   get:
 *     summary: Search countries by name
 *     tags: [Countries]
 *     parameters:
 *       - in: path
 *         name: name
 *         required: true
 *         schema:
 *           type: string
 *         description: Country name to search for
 *     responses:
 *       200:
 *         description: Matching countries
 *         content:
 *           application/json:
 *             schema:
 *               type: array
 *               items:
 *                 $ref: '#/components/schemas/Country'
 */
apiV1Router.get('/countries/search/:name', (req, res) => {
  const searchName = req.params.name.toLowerCase();
  const matchingCountries = countries.filter(c => 
    c.name.toLowerCase().includes(searchName)
  );
  
  res.json(matchingCountries);
});

/**
 * @swagger
 * /data:
 *   get:
 *     summary: Get raw countries data as JSON
 *     tags: [Data]
 *     responses:
 *       200:
 *         description: Raw countries JSON data
 *         content:
 *           application/json:
 *             schema:
 *               type: array
 *               items:
 *                 $ref: '#/components/schemas/Country'
 */
apiV1Router.get('/data', (req, res) => {
  res.setHeader('Content-Type', 'application/json');
  res.json(countries);
});

/**
 * @swagger
 * /coordinates:
 *   get:
 *     summary: Find closest country by coordinates (query params)
 *     tags: [Location]
 *     parameters:
 *       - in: query
 *         name: longitude
 *         required: true
 *         schema:
 *           type: number
 *         description: Longitude coordinate
 *       - in: query
 *         name: latitude
 *         required: true
 *         schema:
 *           type: number
 *         description: Latitude coordinate
 *     responses:
 *       200:
 *         description: Closest country with distance
 *         content:
 *           application/json:
 *             schema:
 *               type: object
 *               properties:
 *                 country:
 *                   $ref: '#/components/schemas/Country'
 *                 distance:
 *                   type: number
 *                   description: Distance in kilometers
 *                 coordinates:
 *                   type: object
 *                   properties:
 *                     longitude:
 *                       type: number
 *                     latitude:
 *                       type: number
 *       400:
 *         description: Invalid coordinates
 *   post:
 *     summary: Find closest country by coordinates (POST body)
 *     tags: [Location]
 *     requestBody:
 *       required: true
 *       content:
 *         application/json:
 *           schema:
 *             type: object
 *             properties:
 *               longitude:
 *                 type: number
 *               latitude:
 *                 type: number
 *             required:
 *               - longitude
 *               - latitude
 *     responses:
 *       200:
 *         description: Closest country with distance
 *         content:
 *           application/json:
 *             schema:
 *               type: object
 *               properties:
 *                 country:
 *                   $ref: '#/components/schemas/Country'
 *                 distance:
 *                   type: number
 *                   description: Distance in kilometers
 *                 coordinates:
 *                   type: object
 *                   properties:
 *                     longitude:
 *                       type: number
 *                     latitude:
 *                       type: number
 *       400:
 *         description: Invalid coordinates
 */
function findClosestCountry(longitude, latitude) {
  let closestCountry = null;
  let minDistance = Infinity;
  
  countries.forEach(country => {
    const [countryLat, countryLng] = country.latlng;
    const distance = calculateDistance(latitude, longitude, countryLat, countryLng);
    
    if (distance < minDistance) {
      minDistance = distance;
      closestCountry = country;
    }
  });
  
  return {
    country: closestCountry,
    distance: Math.round(minDistance * 100) / 100, // Round to 2 decimal places
    coordinates: { longitude, latitude }
  };
}

apiV1Router.get('/coordinates', (req, res) => {
  const { longitude, latitude } = req.query;
  
  if (!longitude || !latitude) {
    return res.status(400).json({ 
      error: 'Both longitude and latitude query parameters are required' 
    });
  }
  
  const lng = parseFloat(longitude);
  const lat = parseFloat(latitude);
  
  if (isNaN(lng) || isNaN(lat) || lng < -180 || lng > 180 || lat < -90 || lat > 90) {
    return res.status(400).json({ 
      error: 'Invalid coordinates. Longitude must be between -180 and 180, latitude between -90 and 90' 
    });
  }
  
  const result = findClosestCountry(lng, lat);
  res.json(result);
});

apiV1Router.post('/coordinates', (req, res) => {
  const { longitude, latitude } = req.body;
  
  if (longitude === undefined || latitude === undefined) {
    return res.status(400).json({ 
      error: 'Both longitude and latitude are required in request body' 
    });
  }
  
  const lng = parseFloat(longitude);
  const lat = parseFloat(latitude);
  
  if (isNaN(lng) || isNaN(lat) || lng < -180 || lng > 180 || lat < -90 || lat > 90) {
    return res.status(400).json({ 
      error: 'Invalid coordinates. Longitude must be between -180 and 180, latitude between -90 and 90' 
    });
  }
  
  const result = findClosestCountry(lng, lat);
  res.json(result);
});

app.use('/api/v1', apiV1Router);

// Non-versioned endpoints
app.get('/api/data', (req, res) => {
  res.setHeader('Content-Type', 'application/json');
  res.json(countries);
});

// Frontend routes
app.get('/', (req, res) => {
  res.render('index', { 
    title: 'Countries API',
    countries: countries 
  });
});

app.get('/country/:code', (req, res) => {
  const code = req.params.code.toUpperCase();
  const country = countries.find(c => c.country_code === code);
  
  if (!country) {
    return res.status(404).render('404', { title: 'Country Not Found' });
  }
  
  res.render('country', { 
    title: `${country.name} - Countries API`,
    country: country 
  });
});

app.get('/location', (req, res) => {
  res.render('location', { 
    title: 'Find Your Nearest Country - Countries API'
  });
});

// 404 handler
app.use((req, res) => {
  res.status(404).render('404', { title: '404 - Page Not Found' });
});

// Error handler
app.use((err, req, res, next) => {
  console.error(err.stack);
  res.status(500).render('error', { 
    title: 'Error',
    error: err.message 
  });
});

const HOST = process.env.HOST || '0.0.0.0';

app.listen(PORT, HOST, () => {
  const displayHost = HOST === '0.0.0.0' ? 'localhost' : HOST;
  console.log(`Server running on ${HOST}:${PORT}`);
  console.log(`Frontend available at http://${displayHost}:${PORT}/`);
  console.log(`Location finder at http://${displayHost}:${PORT}/location`);
  console.log(`API documentation available at http://${displayHost}:${PORT}/docs`);
  console.log(`Swagger JSON available at http://${displayHost}:${PORT}/api/docs`);
});

module.exports = app;