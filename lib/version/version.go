package version

const VERSION = "0.26.10"

// 强制最低版本，与此版本的最低向下兼容性
func GetVersion() string {
	return "0.26.0"
}
