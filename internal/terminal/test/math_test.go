package test

import (
	"kh/internal/terminal"
	"testing"
)

func TestReadableMathMarkdown(t *testing.T) {
	cases := []struct{ input, want string }{
		{`Use $a*b < c$ with *prose*.`, `Use a\*b \< c with *prose*.`},
		{`\(\unknown{x_1}\)`, `\\\(\\unknown\{x\_1\}\\\)`},
		{`\[\unknown{[x]}\]`, `\\\[\\unknown\{\[x\]\}\\\]`},
		{`$x_{q} + \alpha$`, `\$x\_\{q\} \+ \\alpha\$`},
		{`$\frac{a*b}{c} > 1$`, `\(a\*b\)\/\(c\) \> 1`},
		{`$\# + \_$`, `\# \+ \_`},
		{`$- x$`, `\- x`},
		{`$+ x$`, `\+ x`},
		{`\[1. x\]`, `1\. x`},
		{" > ~~~\n > $x^2$\n > ~~~", " > ~~~\n > $x^2$\n > ~~~"},
		{"- ~~~\n  $x^2$\n  ~~~", "- ~~~\n  $x^2$\n  ~~~"},
		{"`$x*y$` and ``\\(x\\)``", "`$x*y$` and ``\\(x\\)``"},
		{"~~~\n\\(x_1\\)\n~~~\n$x*y$", "~~~\n\\(x_1\\)\n~~~\nx\\*y"},
		{"    $x*y$\n\t\\(x_1\\)", "    $x*y$\n\t\\(x_1\\)"},
		{`Cost $5 and $10; [link] and *text*.`, `Cost $5 and $10; [link] and *text*.`},
	}
	for _, tc := range cases {
		if got := terminal.ReadableMathMarkdown(tc.input); got != tc.want {
			t.Errorf("ReadableMathMarkdown(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestReadableMath(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"inline", `Use $x^2 + y_1$ here.`, `Use x² + y₁ here.`},
		{"Greek", `$\alpha + \beta = \Gamma \times \pi$`, `α + β = Γ × π`},
		{"delimiters", `\(x_2\) and \[\sqrt{x+1}\] and $$\frac{a+b}{c}$$`, `x₂ and √(x+1) and (a+b)/(c)`},
		{"nested", `$\frac{\sqrt{x^2}}{\frac{a}{b}}$`, `(√(x²))/((a)/(b))`},
		{"scripts", `$x^{12}+y_{ij}+e^{-x}$`, `x¹²+yᵢⱼ+e⁻ˣ`},
		{"grouping", `${a+b}^2$`, `(a+b)²`},
		{"no lookalike superscript", `$x^q$`, `$x^q$`},
		{"operators", `$a\leq b \neq c \to \infty$`, `a≤ b ≠ c → ∞`},
		{"display lines", "$$\nx^2 + y^2\n$$", "\nx² + y²\n"},
		{"numeric expression", `$2+2=4$`, `2+2=4`},
		{"currency", `It costs $5 and $10, or $20.00. Also $5$ and $1,000$.`, `It costs $5 and $10, or $20.00. Also $5$ and $1,000$.`},
		{"escaped", `\$x^2$ and \\(x^2\\)`, `\$x^2$ and \\(x^2\\)`},
		{"word adjacent", `a$x^2$b and $x^2$ USD`, `a$x^2$b and x² USD`},
		{"spaces", `$ x^2$ and $x^2 $`, `$ x^2$ and $x^2 $`},
		{"unknown command", `$\alpha + \unknown{x}$`, `$\alpha + \unknown{x}$`},
		{"unsupported script", `$x_{q}+\alpha$`, `$x_{q}+\alpha$`},
		{"indexed root", `$\sqrt[3]{x}$`, `$\sqrt[3]{x}$`},
		{"malformed", `$\frac{x}$ and \(x^{2\) and $$x`, `$\frac{x}$ and \(x^{2\) and $$x`},
		{"empty", `$$ and \(\)`, `$$ and \(\)`},
		{"inline code", "`$x^2$` and $y^2$", "`$x^2$` and y²"},
		{"long code span", "`` a `$x^2$` \\(y\\) `` $z_1$", "`` a `$x^2$` \\(y\\) `` z₁"},
		{"multiline code", "`code\n$x^2$`\n$y^2$", "`code\n$x^2$`\ny²"},
		{"backtick fence", "```latex\n$x^2$\n\\(y\\)\n```\n$z^2$", "```latex\n$x^2$\n\\(y\\)\n```\nz²"},
		{"tilde fence", "  ~~~~ latex\r\n$x^2$\r\n~~~\r\n~~~~~\r\n$z^2$", "  ~~~~ latex\r\n$x^2$\r\n~~~\r\n~~~~~\r\nz²"},
		{"unclosed fence", "~~~\n$x^2$", "~~~\n$x^2$"},
		{"unmatched inline", "` unmatched $x^2$", "` unmatched x²"},
		{"unequal inline runs", "` $x^2$ ``", "` x² ``"},
		{"indented code", "    $x^2$\n\t\\(y^2\\)\n\n$x^2$", "    $x^2$\n\t\\(y^2\\)\n\nx²"},
		{"quoted code", "> ~~~\n> $x^2$\n> ~~~", "> ~~~\n> $x^2$\n> ~~~"},
		{"list code", "- ~~~\n  $x^2$\n  ~~~", "- ~~~\n  $x^2$\n  ~~~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := terminal.ReadableMath(tc.input); got != tc.want {
				t.Fatalf("ReadableMath(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
