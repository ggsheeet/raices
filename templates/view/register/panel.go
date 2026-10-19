package register

type Offer struct {
	Base        string
	UnitCents   int
	FixedChurch string
}

type PanelView struct {
	Pay         bool
	CheckoutKey string
	Error       string
	Message     string
	PollPath    string
	Redirect    string
	IFrameURL   string
	LinkURL     string
	PayPath     string
}

type Fact struct {
	Label string
	Value string
}

type Receipt struct {
	OK       bool
	Title    string
	Lead     string
	Detail   string
	Facts    []Fact
	HomePath string
}

func PayView(payPath, checkoutKey, errMsg string) PanelView {
	return PanelView{Pay: true, CheckoutKey: checkoutKey, Error: errMsg, PayPath: payPath}
}
