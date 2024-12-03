const router = require('express').Router();

const sagawiseRoutes = require('./SagawiseRoutes');

// Sagawise Endpoint(s)
router.use('/v1/sagawise', sagawiseRoutes);

module.exports = router;
