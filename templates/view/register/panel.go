package register

type PanelView struct {
	Pay         bool
	CheckoutKey string
	Error       string
	Message     string
	PollPath    string
	Redirect    string
	IFrameURL   string
	LinkURL     string
}

type Fact struct {
	Label string
	Value string
}

type Receipt struct {
	OK     bool
	Title  string
	Lead   string
	Detail string
	Facts  []Fact
}

func PayView(checkoutKey, errMsg string) PanelView {
	return PanelView{Pay: true, CheckoutKey: checkoutKey, Error: errMsg}
}
