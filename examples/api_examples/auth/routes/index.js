const router = require('express').Router();

const authRoutes = require('./AuthRoutes');
const sagawiseRoutes = require('./SagawiseRoutes');


router.use('/v1/auth', authRoutes);

// Sagawise Endpoint(s)
router.use('/v1/sagawise', sagawiseRoutes);

module.exports = router;
