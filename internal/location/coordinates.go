package location

import "math"

const xPi = math.Pi * 3000.0 / 180.0

// GCJ02ToBD09 converts Mars coordinates to Baidu coordinates.
func GCJ02ToBD09(latitude, longitude float64) (float64, float64) {
	x := longitude
	y := latitude
	z := math.Sqrt(x*x+y*y) + 0.00002*math.Sin(y*xPi)
	theta := math.Atan2(y, x) + 0.000003*math.Cos(x*xPi)
	return z*math.Sin(theta) + 0.006, z*math.Cos(theta) + 0.0065
}

// BD09ToGCJ02 converts Baidu coordinates to Mars coordinates.
func BD09ToGCJ02(latitude, longitude float64) (float64, float64) {
	x := longitude - 0.0065
	y := latitude - 0.006
	z := math.Sqrt(x*x+y*y) - 0.00002*math.Sin(y*xPi)
	theta := math.Atan2(y, x) - 0.000003*math.Cos(x*xPi)
	return z * math.Sin(theta), z * math.Cos(theta)
}

// WGS84ToGCJ02 converts GPS coordinates to China's encrypted Mars coordinates.
func WGS84ToGCJ02(latitude, longitude float64) (float64, float64) {
	if outOfChina(latitude, longitude) {
		return latitude, longitude
	}
	dLat, dLng := delta(latitude, longitude)
	return latitude + dLat, longitude + dLng
}

// GCJ02ToWGS84 approximates the inverse of China's coordinate encryption.
func GCJ02ToWGS84(latitude, longitude float64) (float64, float64) {
	if outOfChina(latitude, longitude) {
		return latitude, longitude
	}
	dLat, dLng := delta(latitude, longitude)
	return latitude - dLat, longitude - dLng
}

func outOfChina(latitude, longitude float64) bool {
	return longitude < 72.004 || longitude > 137.8347 || latitude < 0.8293 || latitude > 55.8271
}

func delta(latitude, longitude float64) (float64, float64) {
	dLat := transformLat(longitude-105.0, latitude-35.0)
	dLng := transformLng(longitude-105.0, latitude-35.0)
	radLat := latitude / 180.0 * math.Pi
	magic := math.Sin(radLat)
	magic = 1 - 0.00669342162296594323*magic*magic
	sqrtMagic := math.Sqrt(magic)
	dLat = (dLat * 180.0) / ((6378245.0 / sqrtMagic) * (6378245.0 / sqrtMagic) * math.Pi)
	dLng = (dLng * 180.0) / (6378245.0 / sqrtMagic * math.Cos(radLat) * math.Pi)
	return dLat, dLng
}

func transformLat(x, y float64) float64 {
	value := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	value += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	value += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	value += (160.0*math.Sin(y/12.0*math.Pi) + 320*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return value
}

func transformLng(x, y float64) float64 {
	value := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	value += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	value += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	value += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return value
}
