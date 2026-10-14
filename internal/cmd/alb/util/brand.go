// SPDX-License-Identifier: AGPL-3.0-only

package util

type Brand struct {
	Command    string
	ExitPrefix string
	Product    string
}

var brand = Brand{Command: "alb", ExitPrefix: "ALB", Product: "Application Load Balancers"}

func SetBrand(b Brand) { brand = b }

func CurrentBrand() Brand { return brand }
