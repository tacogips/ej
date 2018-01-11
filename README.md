# JP translator

## install
```
go get github.com/tacogips/ej
```

## usage

get google translate api key from gcp developer console,
and set it into environment variable named `EJ_GOOGLE_TRANS_API_KEY`.

https://cloud.google.com/translate/docs/getting-started

```
export EJ_GOOGLE_TRANS_API_KEY="your_api_key"
```

also using datamuse api to get word definition, synonyms and antonyms

https://www.datamuse.com/api/

```
# eng to jp along with dictionary
> ej love
love
愛
  [word] <love>
  [word]    (def) any object of warm affection or devotion
  [word]    (def) a deep feeling of sexual desire and attraction
  [word]    (def) a strong positive emotion of regard and affection

    [syn] <passion>
    [syn]    (def) strong feeling or emotion
    [syn]    (def) a feeling of strong sexual desire
	...


> ej -nd i am a man
i am a man
私は男です

> ej -nd 我是一个男人
我是一个男人
私は男です

# translate to english if input word detected as japanese
> ej -nd どすこい
Sumo exclamation

# along with dictionary
> ej どすこい
どすこい
Sumo exclamation
  [word] <sumo>
  [word]    (def) a Japanese form of wrestling; you lose if you are forced out of a small ring or if any part of your body (other than your feet) touches the ground

  [word] <exclamation>
  [word]    (def) an abrupt excited utterance
  [word]    (def) a loud complaint or protest or reproach
  [word]    (def) an exclamatory rhetorical device

    [syn] <ecphonesis>
    [syn]    (def) an exclamatory rhetorical device

    [syn] <exclaiming>
    [syn]    (def) an abrupt excited utterance

# show all caches json
> ej -l -json
[{"input":"脆弱a","translated":"Vulnerable"},{"input":"脆弱性","translated":"Vulnerability"}]
```

# Disclaimer
This tool uses google translation api that not cost-free.
Heavy using leads you to bankruptcy.
At your own risk and wallet.

https://cloud.google.com/translate/pricing
