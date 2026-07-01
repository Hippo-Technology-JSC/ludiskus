// Package db nhúng migrations versioned và seed mặc định (board, từ cấm,
// event-type đăng ký lunoti). Xem ludiskus/docs/09-database.md.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS

//go:embed seeds/*.json
var Seeds embed.FS
